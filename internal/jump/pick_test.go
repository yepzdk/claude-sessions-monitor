package jump

import (
	"errors"
	"testing"
)

func TestPick(t *testing.T) {
	claudeTab := candidate{ID: "a", Dir: "/proj/web", Name: "✳ Fix the pagination bug"}
	shellTab := candidate{ID: "b", Dir: "/proj/web", Name: "…/proj/web"}
	otherTab := candidate{ID: "c", Dir: "/proj/api", Name: "✳ Add an endpoint"}

	tests := []struct {
		name        string
		cands       []candidate
		tty         string
		dir         string
		wantID      string
		wantMatches int
	}{
		{
			name:        "tty wins outright",
			cands:       []candidate{{ID: "a", TTY: "/dev/ttys001", Dir: "/proj/web"}, {ID: "b", TTY: "/dev/ttys002", Dir: "/proj/web"}},
			tty:         "/dev/ttys002",
			dir:         "/proj/web",
			wantID:      "b",
			wantMatches: 1,
		},
		{
			name:        "falls back to directory when tty is unknown",
			cands:       []candidate{otherTab, claudeTab},
			dir:         "/proj/web",
			wantID:      "a",
			wantMatches: 1,
		},
		{
			name:        "falls back to directory when no tty matches",
			cands:       []candidate{{ID: "a", TTY: "/dev/ttys009", Dir: "/proj/web", Name: "✳ Working"}},
			tty:         "/dev/ttys002",
			dir:         "/proj/web",
			wantID:      "a",
			wantMatches: 1,
		},
		{
			name:        "prefers the Claude tab over a shell in the same directory",
			cands:       []candidate{shellTab, claudeTab},
			dir:         "/proj/web",
			wantID:      "a",
			wantMatches: 2,
		},
		{
			name:        "reports every directory match so the caller can flag a guess",
			cands:       []candidate{claudeTab, otherTab, shellTab},
			dir:         "/proj/web",
			wantID:      "a",
			wantMatches: 2,
		},
		{
			name:        "falls back to the first tab when every name looks like a path",
			cands:       []candidate{shellTab, {ID: "d", Dir: "/proj/web", Name: "~/proj/web"}},
			dir:         "/proj/web",
			wantID:      "b",
			wantMatches: 2,
		},
		{
			name:        "no match in an unopened directory",
			cands:       []candidate{claudeTab, otherTab},
			dir:         "/proj/nothing",
			wantMatches: 0,
		},
		{
			name:        "no match without a directory to search on",
			cands:       []candidate{claudeTab},
			wantMatches: 0,
		},
		{
			name:        "no match with no terminals open",
			dir:         "/proj/web",
			wantMatches: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, matches := pick(tt.cands, tt.tty, tt.dir)
			if matches != tt.wantMatches {
				t.Errorf("pick() matches = %d, want %d", matches, tt.wantMatches)
			}
			if got.ID != tt.wantID {
				t.Errorf("pick() chose %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

func TestLooksLikePath(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"…/Projects/personal/webwrap", true},
		{"/Users/me/proj", true},
		{"~/proj", true},
		{"", true},
		{"   ", true},
		{"✳ Fix the pagination bug", false},
		{"◐ Issue #48 ghostty changes", false},
		{"npm run dev", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikePath(tt.name); got != tt.want {
				t.Errorf("looksLikePath(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestUnsupportedIsMatchable(t *testing.T) {
	err := unsupportedf("can't jump to %s yet", "iTerm")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("errors.Is(%v, ErrUnsupported) = false, want true", err)
	}
	if got := err.Error(); got != "can't jump to iTerm yet" {
		t.Errorf("Error() = %q, want the sentence alone", got)
	}
}

// A tty match must only happen when the caller passes one. Focus withholds the
// tty unless Session.PIDConfident, because GhostPID is otherwise paired to the
// log file by array position — an "exact" match on a mispaired pid would focus
// a sibling session's tab while reporting full confidence.
func TestPickIgnoresTTYWhenCallerWithholdsIt(t *testing.T) {
	cands := []candidate{
		{ID: "sibling", TTY: "/dev/ttys001", Dir: "/proj/web", Name: "✳ Other session"},
		{ID: "shell", TTY: "/dev/ttys002", Dir: "/proj/web", Name: "…/proj/web"},
	}

	// With a tty, the exact match wins outright and reports a single match.
	if got, matches := pick(cands, "/dev/ttys001", "/proj/web"); got.ID != "sibling" || matches != 1 {
		t.Errorf("pick() with tty = %q/%d, want sibling/1", got.ID, matches)
	}

	// Without one, it falls back to the directory and admits it guessed.
	got, matches := pick(cands, "", "/proj/web")
	if got.ID != "sibling" {
		t.Errorf("pick() without tty chose %q, want the non-path-titled tab", got.ID)
	}
	if matches != 2 {
		t.Errorf("pick() without tty reported %d matches, want 2 so the UI flags the guess", matches)
	}
}
