//go:build darwin

package jump

import "testing"

func TestParseTerminals(t *testing.T) {
	// Ghostty releases without ghostty-org/ghostty#11922 leave the tty field
	// empty; newer ones fill it in. Both shapes have to parse.
	out := "id-1\t/dev/ttys002\t/proj/web\t✳ Fix a bug\n" +
		"id-2\t\t/proj/api\t…/proj/api\n" +
		"\n"

	got := parseTerminals(out)
	if len(got) != 2 {
		t.Fatalf("parseTerminals() returned %d candidates, want 2", len(got))
	}
	if got[0] != (candidate{ID: "id-1", TTY: "/dev/ttys002", Dir: "/proj/web", Name: "✳ Fix a bug"}) {
		t.Errorf("first candidate = %+v", got[0])
	}
	if got[1].TTY != "" || got[1].Dir != "/proj/api" {
		t.Errorf("second candidate = %+v, want empty tty and /proj/api", got[1])
	}
}

func TestParseTerminalsEmpty(t *testing.T) {
	if got := parseTerminals(""); len(got) != 0 {
		t.Errorf("parseTerminals(\"\") = %+v, want none", got)
	}
}
