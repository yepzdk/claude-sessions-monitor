package session

import (
	"encoding/json"
	"testing"
)

func TestExtractContextUsage(t *testing.T) {
	tests := []struct {
		name           string
		entries        []LogEntry
		wantPercent    float64
		wantTokens     int
		wantHasContext bool
	}{
		{
			name:           "empty entries",
			entries:        []LogEntry{},
			wantPercent:    0,
			wantTokens:     0,
			wantHasContext: false,
		},
		{
			name: "no assistant entries",
			entries: []LogEntry{
				{Type: "user"},
			},
			wantPercent:    0,
			wantTokens:     0,
			wantHasContext: false,
		},
		{
			name: "assistant without usage",
			entries: []LogEntry{
				{Type: "assistant", Message: &Message{Role: "assistant"}},
			},
			wantPercent:    0,
			wantTokens:     0,
			wantHasContext: false,
		},
		{
			name: "assistant with usage data",
			entries: []LogEntry{
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:                10,
							CacheCreationInputTokens:   1000,
							CacheReadInputTokens:       19000,
							OutputTokens:               500,
						},
					},
				},
			},
			wantPercent:    10.005, // (10 + 1000 + 19000) / 200000 * 100
			wantTokens:     20010,
			wantHasContext: true,
		},
		{
			name: "uses last entry with usage",
			entries: []LogEntry{
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:              5,
							CacheCreationInputTokens: 500,
							CacheReadInputTokens:     9500,
							OutputTokens:             100,
						},
					},
				},
				{Type: "user"},
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:              10,
							CacheCreationInputTokens: 1000,
							CacheReadInputTokens:     39000,
							OutputTokens:             200,
						},
					},
				},
			},
			wantPercent:    20.005, // (10 + 1000 + 39000) / 200000 * 100
			wantTokens:     40010,
			wantHasContext: true,
		},
		{
			name: "high context usage",
			entries: []LogEntry{
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:              100,
							CacheCreationInputTokens: 10000,
							CacheReadInputTokens:     170000,
							OutputTokens:             1000,
						},
					},
				},
			},
			wantPercent:    90.05, // (100 + 10000 + 170000) / 200000 * 100
			wantTokens:     180100,
			wantHasContext: true,
		},
		{
			name: "compact_boundary after last assistant resets context",
			entries: []LogEntry{
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:              100,
							CacheCreationInputTokens: 10000,
							CacheReadInputTokens:     170000,
							OutputTokens:             1000,
						},
					},
				},
				{Type: "system", Subtype: "compact_boundary"},
			},
			wantPercent:    0,
			wantTokens:     0,
			wantHasContext: false,
		},
		{
			name: "microcompact_boundary after last assistant resets context",
			entries: []LogEntry{
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:              100,
							CacheCreationInputTokens: 10000,
							CacheReadInputTokens:     170000,
							OutputTokens:             1000,
						},
					},
				},
				{Type: "system", Subtype: "microcompact_boundary"},
			},
			wantPercent:    0,
			wantTokens:     0,
			wantHasContext: false,
		},
		{
			name: "assistant after compact_boundary returns correct usage",
			entries: []LogEntry{
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:              100,
							CacheCreationInputTokens: 10000,
							CacheReadInputTokens:     170000,
							OutputTokens:             1000,
						},
					},
				},
				{Type: "system", Subtype: "compact_boundary"},
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-opus-4-6",
						Usage: &Usage{
							InputTokens:              10,
							CacheCreationInputTokens: 1000,
							CacheReadInputTokens:     19000,
							OutputTokens:             500,
						},
					},
				},
			},
			wantPercent:    10.005, // (10 + 1000 + 19000) / 200000 * 100
			wantTokens:     20010,
			wantHasContext: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			percent, tokens := extractContextUsage(tt.entries)
			hasContext := tokens > 0

			if hasContext != tt.wantHasContext {
				t.Errorf("hasContext = %v, want %v", hasContext, tt.wantHasContext)
			}

			if tokens != tt.wantTokens {
				t.Errorf("tokens = %d, want %d", tokens, tt.wantTokens)
			}

			// Compare percentages with small tolerance for floating point
			diff := percent - tt.wantPercent
			if diff < -0.01 || diff > 0.01 {
				t.Errorf("percent = %f, want %f", percent, tt.wantPercent)
			}
		})
	}
}

func TestUsageJSONParsing(t *testing.T) {
	// Test that real JSONL usage data parses correctly
	raw := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":19000,"output_tokens":500,"service_tier":"standard"}}}`

	var entry LogEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatalf("Failed to parse JSONL: %v", err)
	}

	if entry.Message == nil {
		t.Fatal("Message is nil")
	}
	if entry.Message.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if entry.Message.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", entry.Message.Usage.InputTokens)
	}
	if entry.Message.Usage.CacheCreationInputTokens != 1000 {
		t.Errorf("CacheCreationInputTokens = %d, want 1000", entry.Message.Usage.CacheCreationInputTokens)
	}
	if entry.Message.Usage.CacheReadInputTokens != 19000 {
		t.Errorf("CacheReadInputTokens = %d, want 19000", entry.Message.Usage.CacheReadInputTokens)
	}
	if entry.Message.Usage.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", entry.Message.Usage.OutputTokens)
	}
}
