package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserAgent(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	tests := []struct {
		name string
		set  string
		want string
	}{
		{
			name: "unset build keeps a well-formed default",
			set:  "",
			want: "csm/dev (+https://github.com/yepzdk/claude-sessions-monitor)",
		},
		{
			// git describe hands main a leading "v"; the header carries the
			// bare version, matching the Makefile's PKG_VERSION.
			name: "release tag drops the leading v",
			set:  "v0.4.0",
			want: "csm/0.4.0 (+https://github.com/yepzdk/claude-sessions-monitor)",
		},
		{
			name: "untagged build passes through as-is",
			set:  "v0.4.0-3-gabc1234-dirty",
			want: "csm/0.4.0-3-gabc1234-dirty (+https://github.com/yepzdk/claude-sessions-monitor)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version = original
			if tt.set != "" {
				SetVersion(tt.set)
			}
			if got := userAgent(); got != tt.want {
				t.Errorf("userAgent() = %q, want %q", got, tt.want)
			}
			if strings.Contains(userAgent(), "claude-cli") {
				t.Error("userAgent() must not imitate another client")
			}
		})
	}
}

// A corrupt or truncated line can carry a digit run past int range. Unchecked
// accumulation wrapped negative and pulled the 5-hour aggregate down.
func TestExtractIntFieldRejectsOverflow(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{"normal", `{"usage":{"input_tokens":1234}}`, 1234},
		{"zero", `{"usage":{"input_tokens":0}}`, 0},
		{"absent", `{"usage":{"output_tokens":5}}`, 0},
		// Past int64. Unchecked accumulation wrapped this to a negative and
		// pulled the aggregate down.
		{"beyond int range", `{"usage":{"input_tokens":9223372036854775999}}`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractIntField(tt.line, `"input_tokens":`)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
			if got < 0 {
				t.Errorf("negative token count %d would reduce the quota total", got)
			}
		})
	}
}

// bufio.Scanner stops silently on a line past its size cap. Returning the
// partial sum as if the scan completed understates the user's quota with no
// signal at all.
func TestScanLogTokensReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "big.jsonl")

	ts := time.Now().Format(time.RFC3339Nano)
	var b strings.Builder
	b.WriteString(`{"timestamp":"` + ts + `","usage":{"input_tokens":100,"output_tokens":0}}` + "\n")
	// One line past the 10MB scanner cap.
	b.WriteString(`{"timestamp":"` + ts + `","pad":"` + strings.Repeat("x", 11*1024*1024) + `"}` + "\n")
	b.WriteString(`{"timestamp":"` + ts + `","usage":{"input_tokens":900,"output_tokens":0}}` + "\n")

	if err := os.WriteFile(logFile, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	input, _, _, _, err := scanLogTokens(logFile, time.Now().Add(-time.Hour))
	if err == nil {
		t.Fatalf("scan stopped early but reported success; input=%d "+
			"(the 900 after the oversized line was dropped silently)", input)
	}
	if !strings.Contains(err.Error(), logFile) {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestComputeUsageReportsDiscoveryFailure(t *testing.T) {
	// HOME points nowhere, so DiscoverHistory cannot succeed.
	t.Setenv("HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	u := ComputeUsage()
	if u == nil {
		t.Fatal("ComputeUsage returned nil")
	}
	if u.TotalTokens != 0 {
		t.Fatalf("unexpected tokens: %d", u.TotalTokens)
	}
	if u.Err == "" {
		t.Error("discovery failed but Err is empty; the UI renders this as " +
			`"No token usage in the past 5 hours."`)
	}
}
