package session

import (
	"strings"
	"testing"
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
