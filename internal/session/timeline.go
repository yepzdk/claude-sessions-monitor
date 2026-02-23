package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TimelineContent represents a single content block in a timeline entry
type TimelineContent struct {
	Type  string `json:"type"`            // text, tool_use, tool_result
	Text  string `json:"text,omitempty"`
	Tool  string `json:"tool,omitempty"`  // tool name for tool_use
	Input string `json:"input,omitempty"` // stringified JSON for tool_use
}

// TimelineEntry represents a single entry in a session timeline
type TimelineEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Type      string            `json:"type"`                 // user, assistant, system, summary
	Subtype   string            `json:"subtype,omitempty"`
	Model     string            `json:"model,omitempty"`
	Content   []TimelineContent `json:"content,omitempty"`
	Usage     *Usage            `json:"usage,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	GitBranch string            `json:"git_branch,omitempty"`
}

// SessionMetrics contains aggregated metrics for a session log file
type SessionMetrics struct {
	TotalInputTokens         int            `json:"total_input_tokens"`
	TotalOutputTokens        int            `json:"total_output_tokens"`
	TotalCacheCreationTokens int            `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens     int            `json:"total_cache_read_tokens"`
	ToolUsageCounts          map[string]int `json:"tool_usage_counts"`
	UserPromptCount          int            `json:"user_prompt_count"`
	ToolResultCount          int            `json:"tool_result_count"`
	AssistantMessageCount    int            `json:"assistant_message_count"`
	TurnCount                int            `json:"turn_count"`
	CompactCount             int            `json:"compact_count"`
	ContextPercent           float64        `json:"context_percent"`
	ContextTokens            int            `json:"context_tokens"`
	FirstTimestamp           time.Time      `json:"first_timestamp"`
	LastTimestamp             time.Time      `json:"last_timestamp"`
}

// ValidateLogFilePath checks that a log file path is under the Claude projects
// directory and ends with .jsonl. Returns an error if the path is invalid.
func ValidateLogFilePath(filePath string) error {
	projectsDir, err := ClaudeProjectsDir()
	if err != nil {
		return fmt.Errorf("cannot determine projects directory: %w", err)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Evaluate symlinks to prevent traversal
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	realProjectsDir, err := filepath.EvalSymlinks(projectsDir)
	if err != nil {
		return fmt.Errorf("cannot resolve projects directory: %w", err)
	}

	if !strings.HasPrefix(realPath, realProjectsDir+string(filepath.Separator)) {
		return fmt.Errorf("path is not under Claude projects directory")
	}

	if !strings.HasSuffix(realPath, ".jsonl") {
		return fmt.Errorf("path must end with .jsonl")
	}

	return nil
}

// ParseTimeline reads a JSONL log file and returns paginated timeline entries.
// offset is 0-based, limit controls how many entries to return.
// Returns (entries, totalCount, error).
func ParseTimeline(logFile string, offset, limit int) ([]TimelineEntry, int, error) {
	if err := ValidateLogFilePath(logFile); err != nil {
		return nil, 0, err
	}
	return parseTimelineInternal(logFile, offset, limit)
}

func parseTimelineInternal(logFile string, offset, limit int) ([]TimelineEntry, int, error) {
	file, err := os.Open(logFile)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var all []TimelineEntry
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		te := logEntryToTimeline(entry)
		if te != nil {
			all = append(all, *te)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}

	total := len(all)

	// Reverse so newest entries come first
	for i, j := 0, total-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	// Apply pagination
	if offset >= total {
		return []TimelineEntry{}, total, nil
	}

	end := offset + limit
	if end > total {
		end = total
	}

	return all[offset:end], total, nil
}

// ParseMetrics scans a JSONL log file and returns aggregated session metrics.
func ParseMetrics(logFile string) (*SessionMetrics, error) {
	if err := ValidateLogFilePath(logFile); err != nil {
		return nil, err
	}
	return parseMetricsInternal(logFile)
}

func parseMetricsInternal(logFile string) (*SessionMetrics, error) {
	file, err := os.Open(logFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	m := &SessionMetrics{
		ToolUsageCounts: make(map[string]int),
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var lastUsage *Usage

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		// Track timestamps
		if !entry.Timestamp.IsZero() {
			if m.FirstTimestamp.IsZero() || entry.Timestamp.Before(m.FirstTimestamp) {
				m.FirstTimestamp = entry.Timestamp
			}
			if entry.Timestamp.After(m.LastTimestamp) {
				m.LastTimestamp = entry.Timestamp
			}
		}

		switch entry.Type {
		case "user":
			if entry.Message != nil && hasToolResult(entry.Message.Content) {
				m.ToolResultCount++
			} else {
				m.UserPromptCount++
			}

		case "assistant":
			m.AssistantMessageCount++

			if entry.Message != nil {
				// Accumulate token usage
				if entry.Message.Usage != nil {
					u := entry.Message.Usage
					m.TotalInputTokens += u.InputTokens
					m.TotalOutputTokens += u.OutputTokens
					m.TotalCacheCreationTokens += u.CacheCreationInputTokens
					m.TotalCacheReadTokens += u.CacheReadInputTokens
					lastUsage = u
				}

				// Count tool usage
				for _, content := range entry.Message.Content {
					if content.Type == "tool_use" && content.Name != "" {
						m.ToolUsageCounts[content.Name]++
					}
				}
			}

		case "system":
			if entry.Subtype == "turn_duration" {
				m.TurnCount++
			}
			if entry.Subtype == "compact_boundary" || entry.Subtype == "microcompact_boundary" {
				m.CompactCount++
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Calculate context usage from the last usage entry
	if lastUsage != nil {
		totalTokens := lastUsage.InputTokens + lastUsage.CacheCreationInputTokens + lastUsage.CacheReadInputTokens
		m.ContextTokens = totalTokens
		m.ContextPercent = float64(totalTokens) / float64(DefaultContextWindow) * 100
	}

	return m, nil
}

// logEntryToTimeline converts a LogEntry to a TimelineEntry, or nil if skipped
func logEntryToTimeline(entry LogEntry) *TimelineEntry {
	te := &TimelineEntry{
		Timestamp: entry.Timestamp,
		Type:      entry.Type,
		Subtype:   entry.Subtype,
		GitBranch: entry.GitBranch,
	}

	switch entry.Type {
	case "user", "assistant":
		if entry.Message == nil {
			return nil
		}

		// Skip user entries that only contain tool_result content —
		// these are automatic tool responses, not actual user messages,
		// and the tool usage is already visible on the assistant side.
		if entry.Type == "user" && hasToolResult(entry.Message.Content) {
			return nil
		}

		te.Model = entry.Message.Model
		te.Usage = entry.Message.Usage

		for _, c := range entry.Message.Content {
			tc := TimelineContent{
				Type: c.Type,
			}
			switch c.Type {
			case "text":
				tc.Text = c.Text
			case "tool_use":
				tc.Tool = c.Name
				if len(c.Input) > 0 {
					tc.Input = string(c.Input)
				}
			case "tool_result":
				tc.Text = c.Text
			default:
				tc.Text = c.Text
			}
			te.Content = append(te.Content, tc)
		}

	case "summary":
		te.Summary = entry.Summary

	case "system":
		// Include system entries (turn_duration, compact_boundary, etc.)

	default:
		return nil
	}

	return te
}

// hasToolResult returns true if the content items contain a tool_result entry
func hasToolResult(items []ContentItem) bool {
	for _, c := range items {
		if c.Type == "tool_result" {
			return true
		}
	}
	return false
}
