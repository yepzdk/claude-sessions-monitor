package session

import (
	"bufio"
	"math"
	"os"
	"strings"
	"time"
)

// QuotaConfig holds configuration for token quota tracking.
// A zero TokenLimit means quota tracking is disabled.
type QuotaConfig struct {
	TokenLimit  int
	Window      time.Duration
	CountOutput bool // true = count only output tokens; false = count all tokens
}

// QuotaStatus holds the computed quota state for a rolling window.
type QuotaStatus struct {
	TotalTokens  int           `json:"total_tokens"`
	TokenLimit   int           `json:"token_limit"`
	Percent      float64       `json:"percent"`
	WindowStart  time.Time     `json:"window_start"`
	WindowEnd    time.Time     `json:"window_end"`
	RenewsAt     time.Time     `json:"renews_at"`
	RenewsIn     time.Duration `json:"renews_in"`
	SessionCount int           `json:"session_count"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	CacheTokens  int           `json:"cache_tokens"`
	Enabled      bool          `json:"enabled"`
}

// ComputeQuota aggregates token usage across all sessions within the
// configured rolling window and returns the current quota status.
func ComputeQuota(config QuotaConfig) *QuotaStatus {
	if config.TokenLimit <= 0 {
		return &QuotaStatus{Enabled: false}
	}

	now := time.Now()
	windowStart := now.Add(-config.Window)

	// Discover history covering the window (convert to days, round up)
	days := int(math.Ceil(config.Window.Hours() / 24))
	if days < 1 {
		days = 1
	}

	sessions, err := DiscoverHistory(days)
	if err != nil {
		return &QuotaStatus{
			Enabled:     true,
			TokenLimit:  config.TokenLimit,
			WindowStart: windowStart,
			WindowEnd:   now,
		}
	}

	var (
		totalInput   int
		totalOutput  int
		totalCache   int
		sessionCount int
		oldest       time.Time
	)

	for _, s := range sessions {
		// Skip sessions that ended before the window
		if s.EndTime.Before(windowStart) {
			continue
		}

		input, output, cache, hasTokens := scanLogTokens(s.LogFile, windowStart)
		if !hasTokens {
			continue
		}

		totalInput += input
		totalOutput += output
		totalCache += cache
		sessionCount++

		// Track the oldest contributing timestamp for renewal calculation
		if oldest.IsZero() || s.StartTime.Before(oldest) {
			if s.StartTime.After(windowStart) {
				oldest = s.StartTime
			} else {
				oldest = windowStart
			}
		}
	}

	var totalTokens int
	if config.CountOutput {
		totalTokens = totalOutput
	} else {
		totalTokens = totalInput + totalOutput + totalCache
	}

	percent := 0.0
	if config.TokenLimit > 0 {
		percent = float64(totalTokens) / float64(config.TokenLimit) * 100
	}

	renewsAt := now
	renewsIn := time.Duration(0)
	if !oldest.IsZero() && totalTokens > 0 {
		renewsAt = oldest.Add(config.Window)
		renewsIn = renewsAt.Sub(now)
		if renewsIn < 0 {
			renewsIn = 0
		}
	}

	return &QuotaStatus{
		TotalTokens:  totalTokens,
		TokenLimit:   config.TokenLimit,
		Percent:      percent,
		WindowStart:  windowStart,
		WindowEnd:    now,
		RenewsAt:     renewsAt,
		RenewsIn:     renewsIn,
		SessionCount: sessionCount,
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		CacheTokens:  totalCache,
		Enabled:      true,
	}
}

// scanLogTokens scans a JSONL log file for usage entries with timestamps
// within the window and returns aggregated token counts.
func scanLogTokens(logFile string, windowStart time.Time) (input, output, cache int, hasTokens bool) {
	file, err := os.Open(logFile)
	if err != nil {
		return 0, 0, 0, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Fast pre-filter: only process lines that contain usage data
		if !strings.Contains(line, `"usage"`) {
			continue
		}

		// Extract timestamp
		ts := extractTimestampFromLine(line)
		if ts.IsZero() || ts.Before(windowStart) {
			continue
		}

		// Extract token counts using fast string matching
		inputTokens := extractIntField(line, `"input_tokens":`)
		outputTokens := extractIntField(line, `"output_tokens":`)
		cacheCreation := extractIntField(line, `"cache_creation_input_tokens":`)
		cacheRead := extractIntField(line, `"cache_read_input_tokens":`)

		if inputTokens > 0 || outputTokens > 0 || cacheCreation > 0 || cacheRead > 0 {
			input += inputTokens
			output += outputTokens
			cache += cacheCreation + cacheRead
			hasTokens = true
		}
	}

	return input, output, cache, hasTokens
}

// extractIntField extracts an integer value from a JSON line using fast string matching.
// prefix should be like `"field_name":`.
func extractIntField(line, prefix string) int {
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return 0
	}
	start := idx + len(prefix)
	// Skip whitespace
	for start < len(line) && line[start] == ' ' {
		start++
	}
	if start >= len(line) {
		return 0
	}

	// Read digits
	end := start
	for end < len(line) && line[end] >= '0' && line[end] <= '9' {
		end++
	}
	if end == start {
		return 0
	}

	// Manual int parsing (avoids strconv import)
	n := 0
	for i := start; i < end; i++ {
		n = n*10 + int(line[i]-'0')
	}
	return n
}
