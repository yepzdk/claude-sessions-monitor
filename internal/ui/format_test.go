package ui

import (
	"strings"
	"testing"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// visibleWidth counts the runes a terminal actually draws, ignoring ANSI
// colour codes.
func visibleWidth(s string) int {
	var out []rune
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			out = append(out, r)
		}
	}
	return len(out)
}

// The project column is padded to a fixed width. Measuring that padding in
// bytes while truncating in runes shifts every column to the right of a
// non-ASCII project name.
func TestFormatProjectPadsToRuneWidth(t *testing.T) {
	const width = 30
	for _, name := range []string{
		"my-api-service",
		"проект-мониторинг",
		"项目-监控",
		"café-app",
		"a",
		strings.Repeat("ü", 60),
	} {
		got := visibleWidth(formatProject(session.Session{Project: name}, width))
		if got != width {
			t.Errorf("project %q: visible width = %d, want %d", name, got, width)
		}
	}
}

func TestFormatOriginPadsToRuneWidth(t *testing.T) {
	const width = 10
	for _, display := range []string{"Ghostty", "", "iTerm", "Zed", "Ünicode", "非常長的名字"} {
		got := visibleWidth(formatOrigin(session.Origin{Display: display}, width))
		if got != width {
			t.Errorf("origin %q: visible width = %d, want %d", display, got, width)
		}
	}
}

// A row whose log could not be fully read must say so, or its numbers read as
// measurements.
func TestFormatProjectMarksDegradedRow(t *testing.T) {
	out := formatProject(session.Session{Project: "api", Degraded: "permission denied"}, 30)
	if !strings.Contains(out, "[?]") {
		t.Errorf("degraded session is not marked: %q", out)
	}
	if visibleWidth(out) != 30 {
		t.Errorf("visible width = %d, want 30", visibleWidth(out))
	}
}

func TestActiveSessionsKeepsGhosts(t *testing.T) {
	in := []session.Session{
		{Project: "orphan", Status: session.StatusWaiting, IsGhost: true},
		{Project: "done", Status: session.StatusInactive},
		{Project: "live", Status: session.StatusWorking},
	}
	got := ActiveSessions(in)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2 (ghost kept, inactive dropped)", len(got))
	}
	if !got[0].IsGhost {
		t.Error("ghost session was filtered out; its [ghost] badge can never render")
	}
}
