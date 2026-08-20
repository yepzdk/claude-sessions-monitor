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
		{"SS3 arrow up (application cursor mode)", []byte{0x1b, 'O', 'A'}, []rune{KeyUp}},
		{"SS3 arrow down", []byte{0x1b, 'O', 'B'}, []rune{KeyDown}},
		{
			// Consumed whole, not re-emitted: the trailing letter would
			// otherwise reach the key switch and act as a command. Home is
			// ESC O H, and 'H' switches to the history view.
			name: "home key is dropped, not read as 'H'",
			in:   []byte{0x1b, 'O', 'H'},
			want: nil,
		},
		{"unknown CSI is dropped whole", []byte{0x1b, '[', 'Z'}, nil},
		{"CSI with parameters is dropped whole", []byte{0x1b, '[', '1', ';', '5', 'C'}, []rune{KeyRight}},
		{"escape then a real key", []byte{0x1b, '[', 'Z', 'u'}, []rune{'u'}},
		{"truncated escape is consumed", []byte{0x1b, '['}, nil},
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
