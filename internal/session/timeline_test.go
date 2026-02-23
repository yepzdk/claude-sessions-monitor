package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseMetrics(t *testing.T) {
	// Create a temp JSONL file under a fake projects dir
	tmpDir := t.TempDir()

	lines := []string{
		mustJSON(LogEntry{
			Type:      "user",
			Timestamp: time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
			Message:   &Message{Role: "user", Content: []ContentItem{{Type: "text", Text: "hello"}}},
		}),
		mustJSON(LogEntry{
			Type:      "assistant",
			Timestamp: time.Date(2025, 1, 1, 10, 0, 5, 0, time.UTC),
			Message: &Message{
				Role:  "assistant",
				Model: "claude-opus-4-6",
				Content: []ContentItem{
					{Type: "text", Text: "Hi there!"},
					{Type: "tool_use", Name: "Read"},
				},
				Usage: &Usage{
					InputTokens:              100,
					CacheCreationInputTokens: 200,
					CacheReadInputTokens:     300,
					OutputTokens:             50,
				},
			},
		}),
		mustJSON(LogEntry{
			Type:      "system",
			Subtype:   "turn_duration",
			Timestamp: time.Date(2025, 1, 1, 10, 0, 10, 0, time.UTC),
		}),
		mustJSON(LogEntry{
			Type:      "user",
			Timestamp: time.Date(2025, 1, 1, 10, 1, 0, 0, time.UTC),
			Message:   &Message{Role: "user", Content: []ContentItem{{Type: "text", Text: "do more"}}},
		}),
		mustJSON(LogEntry{
			Type:      "assistant",
			Timestamp: time.Date(2025, 1, 1, 10, 1, 5, 0, time.UTC),
			Message: &Message{
				Role:  "assistant",
				Model: "claude-opus-4-6",
				Content: []ContentItem{
					{Type: "tool_use", Name: "Bash"},
					{Type: "tool_use", Name: "Read"},
				},
				Usage: &Usage{
					InputTokens:              500,
					CacheCreationInputTokens: 1000,
					CacheReadInputTokens:     2000,
					OutputTokens:             150,
				},
			},
		}),
		mustJSON(LogEntry{
			Type:      "system",
			Subtype:   "turn_duration",
			Timestamp: time.Date(2025, 1, 1, 10, 1, 10, 0, time.UTC),
		}),
	}

	logFile := filepath.Join(tmpDir, "test.jsonl")
	writeLines(t, logFile, lines)

	m, err := parseMetricsFromFile(logFile)
	if err != nil {
		t.Fatalf("ParseMetrics failed: %v", err)
	}

	if m.UserMessageCount != 2 {
		t.Errorf("UserMessageCount = %d, want 2", m.UserMessageCount)
	}
	if m.AssistantMessageCount != 2 {
		t.Errorf("AssistantMessageCount = %d, want 2", m.AssistantMessageCount)
	}
	if m.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", m.TurnCount)
	}
	if m.TotalInputTokens != 600 {
		t.Errorf("TotalInputTokens = %d, want 600", m.TotalInputTokens)
	}
	if m.TotalOutputTokens != 200 {
		t.Errorf("TotalOutputTokens = %d, want 200", m.TotalOutputTokens)
	}
	if m.TotalCacheCreationTokens != 1200 {
		t.Errorf("TotalCacheCreationTokens = %d, want 1200", m.TotalCacheCreationTokens)
	}
	if m.TotalCacheReadTokens != 2300 {
		t.Errorf("TotalCacheReadTokens = %d, want 2300", m.TotalCacheReadTokens)
	}
	if m.ToolUsageCounts["Read"] != 2 {
		t.Errorf("ToolUsageCounts[Read] = %d, want 2", m.ToolUsageCounts["Read"])
	}
	if m.ToolUsageCounts["Bash"] != 1 {
		t.Errorf("ToolUsageCounts[Bash] = %d, want 1", m.ToolUsageCounts["Bash"])
	}
}

func TestParseTimeline(t *testing.T) {
	tmpDir := t.TempDir()

	lines := []string{
		mustJSON(LogEntry{
			Type:      "user",
			Timestamp: time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
			Message:   &Message{Role: "user", Content: []ContentItem{{Type: "text", Text: "hello"}}},
		}),
		mustJSON(LogEntry{
			Type:      "assistant",
			Timestamp: time.Date(2025, 1, 1, 10, 0, 5, 0, time.UTC),
			Message: &Message{
				Role:    "assistant",
				Model:   "claude-opus-4-6",
				Content: []ContentItem{{Type: "text", Text: "Hi!"}},
				Usage:   &Usage{InputTokens: 100, OutputTokens: 50},
			},
		}),
		mustJSON(LogEntry{
			Type:      "system",
			Subtype:   "turn_duration",
			Timestamp: time.Date(2025, 1, 1, 10, 0, 10, 0, time.UTC),
		}),
	}

	logFile := filepath.Join(tmpDir, "test.jsonl")
	writeLines(t, logFile, lines)

	// Test full fetch
	entries, total, err := parseTimelineFromFile(logFile, 0, 100)
	if err != nil {
		t.Fatalf("ParseTimeline failed: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(entries) != 3 {
		t.Errorf("len(entries) = %d, want 3", len(entries))
	}

	// Verify first entry
	if entries[0].Type != "user" {
		t.Errorf("entries[0].Type = %q, want %q", entries[0].Type, "user")
	}
	if len(entries[0].Content) != 1 || entries[0].Content[0].Text != "hello" {
		t.Errorf("entries[0] content mismatch")
	}

	// Verify second entry
	if entries[1].Type != "assistant" {
		t.Errorf("entries[1].Type = %q, want %q", entries[1].Type, "assistant")
	}
	if entries[1].Model != "claude-opus-4-6" {
		t.Errorf("entries[1].Model = %q, want %q", entries[1].Model, "claude-opus-4-6")
	}

	// Test pagination
	entries, total, err = parseTimelineFromFile(logFile, 1, 1)
	if err != nil {
		t.Fatalf("ParseTimeline paginated failed: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(entries) != 1 {
		t.Errorf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Type != "assistant" {
		t.Errorf("paginated entry type = %q, want %q", entries[0].Type, "assistant")
	}

	// Test offset beyond range
	entries, total, err = parseTimelineFromFile(logFile, 100, 10)
	if err != nil {
		t.Fatalf("ParseTimeline offset beyond failed: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}

func TestLogEntryToTimeline(t *testing.T) {
	// Test tool_use conversion
	entry := LogEntry{
		Type:      "assistant",
		Timestamp: time.Now(),
		Message: &Message{
			Role:  "assistant",
			Model: "claude-opus-4-6",
			Content: []ContentItem{
				{Type: "text", Text: "Let me read that file."},
				{Type: "tool_use", Name: "Read", Input: json.RawMessage(`{"path":"/foo"}`)},
			},
			Usage: &Usage{InputTokens: 100, OutputTokens: 50},
		},
	}

	te := logEntryToTimeline(entry)
	if te == nil {
		t.Fatal("expected non-nil timeline entry")
	}

	if len(te.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(te.Content))
	}

	if te.Content[0].Type != "text" || te.Content[0].Text != "Let me read that file." {
		t.Errorf("content[0] mismatch: %+v", te.Content[0])
	}
	if te.Content[1].Type != "tool_use" || te.Content[1].Tool != "Read" {
		t.Errorf("content[1] mismatch: %+v", te.Content[1])
	}
	if te.Content[1].Input != `{"path":"/foo"}` {
		t.Errorf("content[1].Input = %q", te.Content[1].Input)
	}

	// Test nil message returns nil
	nilEntry := LogEntry{Type: "user", Message: nil}
	if logEntryToTimeline(nilEntry) != nil {
		t.Error("expected nil for entry with nil message")
	}

	// Test summary entry
	summaryEntry := LogEntry{
		Type:    "summary",
		Summary: "A summary of the session",
	}
	te = logEntryToTimeline(summaryEntry)
	if te == nil {
		t.Fatal("expected non-nil for summary entry")
	}
	if te.Summary != "A summary of the session" {
		t.Errorf("summary = %q", te.Summary)
	}
}

// parseTimelineFromFile is a helper that bypasses path validation for testing
func parseTimelineFromFile(logFile string, offset, limit int) ([]TimelineEntry, int, error) {
	return parseTimelineInternal(logFile, offset, limit)
}

// parseMetricsFromFile is a helper that bypasses path validation for testing
func parseMetricsFromFile(logFile string) (*SessionMetrics, error) {
	return parseMetricsInternal(logFile)
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatalf("write line: %v", err)
		}
	}
}
