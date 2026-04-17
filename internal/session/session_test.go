package session

import (
	"encoding/json"
	"testing"
	"time"
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
			wantPercent:    2.051, // (10 + 1000 + 19000 + 500) / 1000000 * 100 (opus-4-6 = 1M)
			wantTokens:     20510,
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
			wantPercent:    4.021, // (10 + 1000 + 39000 + 200) / 1000000 * 100 (opus-4-6 = 1M)
			wantTokens:     40210,
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
			wantPercent:    18.11, // (100 + 10000 + 170000 + 1000) / 1000000 * 100 (opus-4-6 = 1M)
			wantTokens:     181100,
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
			wantPercent:    2.051, // (10 + 1000 + 19000 + 500) / 1000000 * 100 (opus-4-6 = 1M)
			wantTokens:     20510,
			wantHasContext: true,
		},
		{
			name: "haiku uses 200K context window",
			entries: []LogEntry{
				{
					Type: "assistant",
					Message: &Message{
						Role:  "assistant",
						Model: "claude-haiku-4-5-20251001",
						Usage: &Usage{
							InputTokens:              10,
							CacheCreationInputTokens: 1000,
							CacheReadInputTokens:     19000,
							OutputTokens:             500,
						},
					},
				},
			},
			wantPercent:    10.255, // (10 + 1000 + 19000 + 500) / 200000 * 100 (haiku = 200K)
			wantTokens:     20510,
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

func TestContextWindowForModel(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"claude-opus-4-6", 1_000_000},
		{"opus", 200_000},
		{"claude-sonnet-4-6", 1_000_000},
		{"sonnet", 200_000},
		{"claude-haiku-4-5-20251001", 200_000},
		{"haiku", 200_000},
		{"claude-sonnet-4-5-20250929", 200_000},
		{"", 200_000},
		{"unknown-model", 200_000},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := contextWindowForModel(tt.model)
			if got != tt.want {
				t.Errorf("contextWindowForModel(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestLogEntryNewFields(t *testing.T) {
	// Test that cwd and customTitle fields parse from JSONL
	raw := `{"type":"user","timestamp":"2025-01-01T00:00:00Z","cwd":"/home/user/projects/myapp"}`
	var entry LogEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	if entry.CWD != "/home/user/projects/myapp" {
		t.Errorf("CWD = %q, want %q", entry.CWD, "/home/user/projects/myapp")
	}

	// Test custom-title entry
	raw2 := `{"type":"custom-title","customTitle":"add-linux-support","sessionId":"abc-123"}`
	var entry2 LogEntry
	if err := json.Unmarshal([]byte(raw2), &entry2); err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	if entry2.Type != "custom-title" {
		t.Errorf("Type = %q, want %q", entry2.Type, "custom-title")
	}
	if entry2.CustomTitle != "add-linux-support" {
		t.Errorf("CustomTitle = %q, want %q", entry2.CustomTitle, "add-linux-support")
	}
}

func TestDecodeProjectName(t *testing.T) {
	tests := []struct {
		name string
		input string
		want string
	}{
		{
			name:  "macOS with Projects marker",
			input: "-Users-jesperpedersen-Projects-personal-claude-sessions-monitor",
			want:  "personal/claude-sessions-monitor",
		},
		{
			name:  "macOS without Projects marker",
			input: "-Users-jesperpedersen-some-folder",
			want:  "some/folder",
		},
		{
			name:  "Linux home path",
			input: "-home-user-repos-myproject",
			want:  "home/user/repos/myproject",
		},
		{
			name:  "Linux with Projects marker",
			input: "-home-user-Projects-org-myapp",
			want:  "org/myapp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeProjectName(tt.input)
			if got != tt.want {
				t.Errorf("decodeProjectName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractProjectName(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "macOS with Projects",
			path: "/Users/username/Projects/org/project",
			want: "org/project",
		},
		{
			name: "Linux home path with repos marker",
			path: "/home/user/repos/myproject",
			want: "myproject",
		},
		{
			name: "Linux with Projects",
			path: "/home/user/Projects/work/myapp",
			want: "work/myapp",
		},
		{
			name: "Linux home with nested path",
			path: "/home/user/myproject",
			want: "myproject",
		},
		{
			name: "repos marker",
			path: "/opt/repos/org/myapp",
			want: "org/myapp",
		},
		{
			name: "src marker",
			path: "/var/src/backend-api",
			want: "backend-api",
		},
		{
			name: "code marker",
			path: "/home/dev/code/org/tool",
			want: "org/tool",
		},
		{
			name: "workspace marker",
			path: "/mnt/workspace/project-x",
			want: "project-x",
		},
		{
			name: "two components fallback",
			path: "/opt/myproject",
			want: "opt/myproject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractProjectName(tt.path)
			if got != tt.want {
				t.Errorf("extractProjectName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestEncodeProjectPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "simple path",
			path: "/Users/username/Projects/org/project",
			want: "-Users-username-Projects-org-project",
		},
		{
			name: "path with underscores",
			path: "/Users/username/Projects/org/my_app_api",
			want: "-Users-username-Projects-org-my-app-api",
		},
		{
			name: "path with multiple underscores",
			path: "/Users/username/Projects/org/my_newsletter_templates",
			want: "-Users-username-Projects-org-my-newsletter-templates",
		},
		{
			name: "path with dots",
			path: "/Users/username/Projects/org/my.project",
			want: "-Users-username-Projects-org-my-project",
		},
		{
			name: "path with dots and underscores",
			path: "/Users/username/Projects/org/my_project.v2",
			want: "-Users-username-Projects-org-my-project-v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeProjectPath(tt.path)
			if got != tt.want {
				t.Errorf("encodeProjectPath(%q) = %q, want %q", tt.path, got, tt.want)
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

func TestDetermineStatus(t *testing.T) {
	now := time.Now()

	// Helper to create a timestamp relative to now
	ago := func(d time.Duration) time.Time {
		return now.Add(-d)
	}

	// Zero time means "no file modtime" — won't trigger the file modtime check
	zeroTime := time.Time{}

	tests := []struct {
		name        string
		entries     []LogEntry
		isRunning   bool
		fileModTime time.Time
		wantStatus  Status
		wantTask    string
	}{
		{
			name:       "empty entries not running",
			entries:    nil,
			isRunning:   false,
			fileModTime: zeroTime,
			wantStatus:  StatusInactive,
			wantTask:    "-",
		},
		{
			name:        "empty entries running",
			entries:     nil,
			isRunning:   true,
			fileModTime: zeroTime,
			wantStatus:  StatusWaiting,
			wantTask:    "-",
		},
		{
			name: "not running with entries",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(10 * time.Second)},
			},
			isRunning:  false,
			wantStatus: StatusInactive,
			wantTask:   "-",
		},
		{
			name: "recent assistant text message within 2 minutes",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(45 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Working on it"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Working on it",
		},
		{
			name: "assistant text message older than 2 minutes",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(3 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Working on it"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWaiting,
			wantTask:   "-",
		},
		{
			name: "pending tool_use recent",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(30 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "tool_use", Name: "Bash"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Using: Bash",
		},
		{
			name: "pending tool_use stale",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(3 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "tool_use", Name: "Bash"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusNeedsInput,
			wantTask:   "Using: Bash",
		},
		{
			name: "tool_use with tool_result and still processing",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(20 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "tool_use", Name: "Read"}},
				}},
				{Type: "user", Timestamp: ago(15 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "tool_result"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Processing...",
		},
		{
			name: "tool_use with tool_result and turn completed",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(30 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "tool_use", Name: "Read"}},
				}},
				{Type: "user", Timestamp: ago(25 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "tool_result"}},
				}},
				{Type: "system", Subtype: "turn_duration", Timestamp: ago(20 * time.Second)},
			},
			isRunning:  true,
			wantStatus: StatusWaiting,
			wantTask:   "-",
		},
		{
			name: "multiple tool_use first resolved second pending",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(30 * time.Second), Message: &Message{
					Content: []ContentItem{
						{Type: "tool_use", Name: "Read"},
						{Type: "tool_use", Name: "Grep"},
					},
				}},
				{Type: "user", Timestamp: ago(25 * time.Second), Message: &Message{
					Content: []ContentItem{
						{Type: "tool_result"},
					},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Using: Grep",
		},
		{
			name: "multiple tool_use all resolved",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(20 * time.Second), Message: &Message{
					Content: []ContentItem{
						{Type: "tool_use", Name: "Read"},
						{Type: "tool_use", Name: "Grep"},
					},
				}},
				{Type: "user", Timestamp: ago(15 * time.Second), Message: &Message{
					Content: []ContentItem{
						{Type: "tool_result"},
						{Type: "tool_result"},
					},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Processing...",
		},
		{
			name: "recent progress heartbeat overrides stale assistant",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(4 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Running tests"}},
				}},
				{Type: "progress", Timestamp: ago(30 * time.Second)},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Running tests",
		},
		{
			name: "hook_progress counts as heartbeat",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(3 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Editing files"}},
				}},
				{Type: "hook_progress", Timestamp: ago(20 * time.Second)},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Editing files",
		},
		{
			name: "agent_progress counts as heartbeat",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(3 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Exploring code"}},
				}},
				{Type: "agent_progress", Timestamp: ago(45 * time.Second)},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Exploring code",
		},
		{
			name: "stale progress does not help",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(4 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Done"}},
				}},
				{Type: "progress", Timestamp: ago(3 * time.Minute)},
			},
			isRunning:  true,
			wantStatus: StatusWaiting,
			wantTask:   "-",
		},
		{
			name: "stale log over 5 minutes",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(6 * time.Minute)},
			},
			isRunning:  true,
			wantStatus: StatusWaiting,
			wantTask:   "-",
		},
		{
			name: "turn completed waiting for user",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(3 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Done"}},
				}},
				{Type: "system", Subtype: "turn_duration", Timestamp: ago(3 * time.Minute)},
			},
			isRunning:  true,
			wantStatus: StatusWaiting,
			wantTask:   "-",
		},
		{
			name: "turn completed then new user message",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(3 * time.Minute)},
				{Type: "system", Subtype: "turn_duration", Timestamp: ago(3 * time.Minute)},
				{Type: "user", Timestamp: ago(10 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Do more"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Processing...",
		},
		{
			name: "user message is most recent no assistant yet",
			entries: []LogEntry{
				{Type: "user", Timestamp: ago(5 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Hello"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Processing...",
		},
		{
			name: "user message after old assistant",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(4 * time.Minute)},
				{Type: "user", Timestamp: ago(3 * time.Second), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Next task"}},
				}},
			},
			isRunning:  true,
			wantStatus: StatusWorking,
			wantTask:   "Processing...",
		},
		{
			name: "recent file modtime overrides stale entries",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(6 * time.Minute), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Building project"}},
				}},
			},
			isRunning:   true,
			fileModTime: ago(10 * time.Second),
			wantStatus:  StatusWorking,
			wantTask:    "Building project",
		},
		{
			name: "old file modtime does not override stale entries",
			entries: []LogEntry{
				{Type: "assistant", Timestamp: ago(6 * time.Minute)},
			},
			isRunning:   true,
			fileModTime: ago(3 * time.Minute),
			wantStatus:  StatusWaiting,
			wantTask:    "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modTime := tt.fileModTime
			if modTime.IsZero() {
				// Default to old modtime so the file modtime check doesn't fire
				modTime = now.Add(-1 * time.Hour)
			}
			status, task, _ := determineStatus(tt.entries, tt.isRunning, modTime)
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if task != tt.wantTask {
				t.Errorf("task = %q, want %q", task, tt.wantTask)
			}
		})
	}
}
