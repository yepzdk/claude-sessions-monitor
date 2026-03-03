package session

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// UsageStats holds aggregated local token usage across sessions within a rolling window.
type UsageStats struct {
	WindowStart  time.Time      `json:"window_start"`
	WindowEnd    time.Time      `json:"window_end"`
	InputTokens  int            `json:"input_tokens"`
	OutputTokens int            `json:"output_tokens"`
	CacheTokens  int            `json:"cache_tokens"`
	TotalTokens  int            `json:"total_tokens"`
	Sessions     []SessionUsage `json:"sessions"`
}

// SessionUsage holds token usage for a single session.
type SessionUsage struct {
	Project      string    `json:"project"`
	LogFile      string    `json:"log_file"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CacheTokens  int       `json:"cache_tokens"`
	TotalTokens  int       `json:"total_tokens"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
}

// APIQuota holds the response from the Anthropic usage API.
type APIQuota struct {
	Available      bool         `json:"available"`
	FiveHour       *QuotaBucket `json:"five_hour"`
	SevenDay       *QuotaBucket `json:"seven_day"`
	SevenDaySonnet *QuotaBucket `json:"seven_day_sonnet,omitempty"`
	SevenDayOpus   *QuotaBucket `json:"seven_day_opus,omitempty"`
	ExtraUsage     *ExtraUsage  `json:"extra_usage,omitempty"`
	Error          string       `json:"error,omitempty"`
}

// QuotaBucket holds utilization data for a single quota window.
// Utilization is a percentage (0-100) as returned by the Anthropic API.
type QuotaBucket struct {
	Utilization float64    `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

// ExtraUsage holds extra usage configuration.
type ExtraUsage struct {
	IsEnabled bool `json:"is_enabled"`
}

// ComputeUsage aggregates token usage across all sessions within a 5-hour rolling window.
func ComputeUsage() *UsageStats {
	now := time.Now()
	windowStart := now.Add(-5 * time.Hour)

	// Discover history covering the window (1 day is enough for 5h)
	sessions, err := DiscoverHistory(1)
	if err != nil {
		return &UsageStats{
			WindowStart: windowStart,
			WindowEnd:   now,
		}
	}

	var (
		totalInput   int
		totalOutput  int
		totalCache   int
		sessionUsage []SessionUsage
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

		sessionUsage = append(sessionUsage, SessionUsage{
			Project:      s.Project,
			LogFile:      s.LogFile,
			InputTokens:  input,
			OutputTokens: output,
			CacheTokens:  cache,
			TotalTokens:  input + output + cache,
			StartTime:    s.StartTime,
			EndTime:      s.EndTime,
		})

		totalInput += input
		totalOutput += output
		totalCache += cache
	}

	return &UsageStats{
		WindowStart:  windowStart,
		WindowEnd:    now,
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		CacheTokens:  totalCache,
		TotalTokens:  totalInput + totalOutput + totalCache,
		Sessions:     sessionUsage,
	}
}

// apiQuotaCache holds a cached API quota response with a TTL.
var apiQuotaCache struct {
	sync.Mutex
	result    *APIQuota
	fetchedAt time.Time
}

const apiQuotaCacheTTL = 30 * time.Second

// FetchAPIQuota queries the Anthropic usage API for real quota utilization.
// Results are cached for 30 seconds to avoid excessive API calls.
func FetchAPIQuota() *APIQuota {
	apiQuotaCache.Lock()
	defer apiQuotaCache.Unlock()

	// Return cached result if fresh
	if apiQuotaCache.result != nil && time.Since(apiQuotaCache.fetchedAt) < apiQuotaCacheTTL {
		return apiQuotaCache.result
	}

	result := fetchAPIQuotaUncached()
	apiQuotaCache.result = result
	apiQuotaCache.fetchedAt = time.Now()
	return result
}

func fetchAPIQuotaUncached() *APIQuota {
	token := GetOAuthToken()
	if token == nil {
		return &APIQuota{Available: false, Error: "OAuth token not found"}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return &APIQuota{Available: false, Error: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := client.Do(req)
	if err != nil {
		return &APIQuota{Available: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &APIQuota{Available: false, Error: err.Error()}
	}

	if resp.StatusCode != http.StatusOK {
		return &APIQuota{Available: false, Error: "API returned " + resp.Status}
	}

	return parseAPIQuotaResponse(body)
}

// parseAPIQuotaResponse parses the JSON response from the Anthropic usage API.
func parseAPIQuotaResponse(body []byte) *APIQuota {
	// The API response structure
	var raw struct {
		FiveHour *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
		SevenDaySonnet *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day_sonnet"`
		SevenDayOpus *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day_opus"`
		ExtraUsage *struct {
			IsEnabled bool `json:"is_enabled"`
		} `json:"extra_usage"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return &APIQuota{Available: false, Error: "failed to parse API response"}
	}

	result := &APIQuota{Available: true}

	if raw.FiveHour != nil {
		bucket := &QuotaBucket{Utilization: raw.FiveHour.Utilization}
		if t, err := time.Parse(time.RFC3339, raw.FiveHour.ResetsAt); err == nil {
			bucket.ResetsAt = &t
		}
		result.FiveHour = bucket
	}

	if raw.SevenDay != nil {
		bucket := &QuotaBucket{Utilization: raw.SevenDay.Utilization}
		if t, err := time.Parse(time.RFC3339, raw.SevenDay.ResetsAt); err == nil {
			bucket.ResetsAt = &t
		}
		result.SevenDay = bucket
	}

	if raw.SevenDaySonnet != nil {
		bucket := &QuotaBucket{Utilization: raw.SevenDaySonnet.Utilization}
		if t, err := time.Parse(time.RFC3339, raw.SevenDaySonnet.ResetsAt); err == nil {
			bucket.ResetsAt = &t
		}
		result.SevenDaySonnet = bucket
	}

	if raw.SevenDayOpus != nil {
		bucket := &QuotaBucket{Utilization: raw.SevenDayOpus.Utilization}
		if t, err := time.Parse(time.RFC3339, raw.SevenDayOpus.ResetsAt); err == nil {
			bucket.ResetsAt = &t
		}
		result.SevenDayOpus = bucket
	}

	if raw.ExtraUsage != nil {
		result.ExtraUsage = &ExtraUsage{IsEnabled: raw.ExtraUsage.IsEnabled}
	}

	return result
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

