package ui

import (
	"slices"
	"testing"
)

func TestDecodeKeys(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []rune
	}{
		{"plain letter", []byte("h"), []rune{'h'}},
		{"ctrl-c", []byte{3}, []rune{3}},
		{"enter", []byte{'\r'}, []rune{KeyEnter}},
		{"arrow up", []byte{0x1b, '[', 'A'}, []rune{KeyUp}},
		{"arrow down", []byte{0x1b, '[', 'B'}, []rune{KeyDown}},
		{"arrow right", []byte{0x1b, '[', 'C'}, []rune{KeyRight}},
		{"arrow left", []byte{0x1b, '[', 'D'}, []rune{KeyLeft}},
		{
			name: "several keys in one read",
			in:   append(append([]byte{0x1b, '[', 'B'}, 0x1b, '[', 'B'), '\r'),
			want: []rune{KeyDown, KeyDown, KeyEnter},
		},
		{"letters around an arrow", []byte{'h', 0x1b, '[', 'A', 'u'}, []rune{'h', KeyUp, 'u'}},
		{
			// An unrecognised CSI sequence must not be swallowed silently, or a
			// key we don't handle would vanish instead of being ignored upstream.
			name: "unknown CSI passes through as bytes",
			in:   []byte{0x1b, '[', 'Z'},
			want: []rune{0x1b, '[', 'Z'},
		},
		{"truncated escape", []byte{0x1b, '['}, []rune{0x1b, '['}},
		{"bare escape", []byte{0x1b}, []rune{0x1b}},
		{"empty read", []byte{}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeKeys(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("decodeKeys(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
