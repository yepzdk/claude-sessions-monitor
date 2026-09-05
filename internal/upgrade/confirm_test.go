package upgrade

import (
	"strings"
	"testing"
)

// Everything that is not an explicit yes leaves the binary alone. A bare Enter
// is the answer someone gives when they were not sure what they were agreeing
// to, and EOF is stdin closing mid-question.
func TestPromptTakesOnlyYesForYes(t *testing.T) {
	tests := []struct {
		typed string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{" y \n", true},
		{"\n", false},
		{"n\n", false},
		{"no\n", false},
		{"", false}, // EOF: stdin closed without an answer
		{"maybe\n", false},
	}

	for _, tt := range tests {
		var out strings.Builder
		got := newPrompt(strings.NewReader(tt.typed), true, false)(&out, "Continue?")
		if got != tt.want {
			t.Errorf("answering %q = %v, want %v", tt.typed, got, tt.want)
		}
		if !strings.Contains(out.String(), "Continue? [y/N]") {
			t.Errorf("the question was not asked, or does not show that no is the default: %q", out.String())
		}
	}
}

// A prompt nobody can answer hangs CI and `curl | sh`. Proceeding is the lesser
// evil, and the question must not even be printed -- it would be a question in
// a log file that nothing ever answered.
func TestPromptSkippedWhenNobodyCanAnswer(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		assumeYes   bool
	}{
		{"non-interactive stdin", false, false},
		{"-y on a terminal", true, true},
		{"-y without a terminal", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// "n" would decline if it were ever read; it must not be.
			in := strings.NewReader("n\n")

			var out strings.Builder
			if !newPrompt(in, tt.interactive, tt.assumeYes)(&out, "Continue?") {
				t.Error("the upgrade was declined by an answer nobody gave")
			}
			if out.String() != "" {
				t.Errorf("a question was printed that nothing could answer: %q", out.String())
			}
			if in.Len() != len("n\n") {
				t.Error("stdin was consumed by a prompt that was not asked")
			}
		})
	}
}
