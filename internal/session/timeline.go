package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TimelineContent represents a single content block in a timeline entry
type TimelineContent struct {
	Type  string `json:"type"` // text, tool_use, tool_result
	Text  string `json:"text,omitempty"`
	Tool  string `json:"tool,omitempty"`  // tool name for tool_use
	Input string `json:"input,omitempty"` // stringified JSON for tool_use
	// ToolUseID is the call's id: the tool_use block's own id, or the id a
	// tool_result answers. It is what lets a flag name the entry it came from,
	// so a flag list can point into the timeline instead of describing a
	// position in it.
	ToolUseID string `json:"tool_use_id,omitempty"`
}

// TimelineEntry represents a single entry in a session timeline
type TimelineEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Type      string            `json:"type"` // user, assistant, system, summary
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
	LastTimestamp            time.Time      `json:"last_timestamp"`
}

// ValidateLogFilePath checks that a log file path is under the Claude projects
// directory and ends with .jsonl. Returns an error if the path is invalid.
//
// Claude-only, because its callers -- ParseTimeline and ParseMetrics -- decode
// the Claude Code format and would report an omp log as an empty session rather
// than as the wrong format. Readers that handle both harnesses use
// validateSessionLogPath.
func ValidateLogFilePath(filePath string) error {
	projectsDir, err := ClaudeProjectsDir()
	if err != nil {
		return fmt.Errorf("cannot determine projects directory: %w", err)
	}
	root, err := logPathRoot(filePath, projectsDir)
	if err != nil {
		return err
	}
	if root == "" {
		return fmt.Errorf("path is not under Claude projects directory")
	}
	return nil
}

// validateSessionLogPath accepts a log under either harness's store and returns
// the root it sits under, so the caller can pick the right reader for it.
func validateSessionLogPath(filePath string) (string, error) {
	var roots []string
	if dir, err := ClaudeProjectsDir(); err == nil {
		roots = append(roots, dir)
	}
	if dir, err := ompSessionsDir(); err == nil {
		roots = append(roots, dir)
	}
	root, err := logPathRoot(filePath, roots...)
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("path is not under a known session store")
	}
	return root, nil
}

// logPathRoot resolves filePath and returns whichever root contains it, or an
// empty string when none does.
//
// Symlinks are evaluated on both sides before comparing: a prefix check against
// an unresolved path is defeated by a symlink pointing out of the store, and
// these paths arrive from an HTTP query parameter.
func logPathRoot(filePath string, roots ...string) (string, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	if !strings.HasSuffix(realPath, ".jsonl") {
		return "", fmt.Errorf("path must end with .jsonl")
	}
	for _, root := range roots {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue // a store that does not exist cannot contain the path
		}
		if strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) {
			return realRoot, nil
		}
	}
	return "", nil
}

// ParseTimeline reads a JSONL log file and returns paginated timeline entries.
// offset is 0-based, limit controls how many entries to return. entryType keeps
// only entries of that type; an empty string keeps every type.
// Returns (entries, totalCount, error).
//
// The type filter is applied before paging, so offset and total both count
// matching entries rather than raw ones. Filtering after paging instead leaves
// a caller paging through a set it cannot see the size of: user turns are a few
// percent of a session, so whole pages of raw entries hold none of them and the
// page a caller asked for comes back empty.
func ParseTimeline(logFile string, offset, limit int, entryType string) ([]TimelineEntry, int, error) {
	if err := ValidateLogFilePath(logFile); err != nil {
		return nil, 0, err
	}
	return parseTimelineInternal(logFile, offset, limit, entryType)
}

func parseTimelineInternal(logFile string, offset, limit int, entryType string) ([]TimelineEntry, int, error) {
	file, err := os.Open(logFile)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()

	scanner := newLogScanner(file, maxLogLineBytes)

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
		if te != nil && (entryType == "" || te.Type == entryType) {
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
	defer func() { _ = file.Close() }()

	m := &SessionMetrics{
		ToolUsageCounts: make(map[string]int),
	}

	scanner := newLogScanner(file, maxLogLineBytes)

	var lastUsage *Usage
	var lastUsageModel string

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
					lastUsageModel = entry.Message.Model
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
				lastUsage = nil
				lastUsageModel = ""
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Calculate context usage from the last usage entry
	m.ContextPercent, m.ContextTokens = contextUsage(lastUsage, lastUsageModel)

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
				tc.ToolUseID = c.ID
				if len(c.Input) > 0 {
					tc.Input = string(c.Input)
				}
			case "tool_result":
				tc.Text = c.Text
				tc.ToolUseID = c.ToolUseID
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
