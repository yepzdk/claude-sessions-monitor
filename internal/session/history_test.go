package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{"short string is returned unchanged", "hello", 120, "hello"},
		{"exactly maxLen is not truncated", strings.Repeat("x", 10), 10, strings.Repeat("x", 10)},
		{"long ascii is cut with an ellipsis", strings.Repeat("x", 20), 10, strings.Repeat("x", 7) + "..."},
		{"empty string", "", 120, ""},

		// The limit counts characters, not bytes, so a non-ASCII prompt gets the
		// same amount of text as an ASCII one rather than a third of it.
		{"multi-byte counts as one rune each", strings.Repeat("é", 10), 10, strings.Repeat("é", 10)},
		{"multi-byte is cut by rune", strings.Repeat("日", 20), 10, strings.Repeat("日", 7) + "..."},

		// Guards: the byte-slicing version panicked on maxLen < 3.
		{"zero maxLen", "hello", 0, ""},
		{"negative maxLen", "hello", -5, ""},
		{"maxLen below the ellipsis width", "hello", 2, "he"},
		{"maxLen exactly the ellipsis width", "hello", 3, "hel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateString(tt.in, tt.maxLen); got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.in, tt.maxLen, got, tt.want)
			}
		})
	}
}

// The bug behind #68: slicing by byte split a rune, so a preview of non-ASCII
// text ended in a replacement character.
func TestTruncateStringKeepsValidUTF8(t *testing.T) {
	inputs := map[string]string{
		"cjk":     "a" + strings.Repeat("日", 60),
		"accents": strings.Repeat("é", 80),
		"emoji":   "b" + strings.Repeat("🙂", 40),
		"mixed":   "café " + strings.Repeat("日本語 ", 30),
	}

	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			// Sweep lengths so the cut lands at every offset within a rune,
			// rather than only where it happens to align.
			for maxLen := 4; maxLen <= 120; maxLen++ {
				got := truncateString(in, maxLen)
				if !utf8.ValidString(got) {
					t.Fatalf("truncateString(%s, %d) produced invalid UTF-8: %q", name, maxLen, got)
				}
				if n := utf8.RuneCountInString(got); n > maxLen {
					t.Fatalf("truncateString(%s, %d) returned %d runes, want at most %d", name, maxLen, n, maxLen)
				}
			}
		})
	}
}
