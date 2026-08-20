package ui

import (
	"os"

	"golang.org/x/term"
)

// Keys that have no single-byte representation, mapped into the Unicode
// private use area so the key channel can stay a plain rune.
const (
	KeyUp rune = 0xE000 + iota
	KeyDown
	KeyRight
	KeyLeft
)

// KeyEnter is Return in raw mode, where the terminal no longer translates CR.
const KeyEnter rune = '\r'

var originalState *term.State

// SetupRawInput puts the terminal into raw mode for single-key input
func SetupRawInput() error {
	var err error
	originalState, err = term.MakeRaw(int(os.Stdin.Fd()))
	return err
}

// CleanupRawInput restores the terminal to its original state
func CleanupRawInput() {
	if originalState != nil {
		term.Restore(int(os.Stdin.Fd()), originalState)
	}
}

// ReadKey reads keypresses from stdin and sends them to keyCh.
//
// Arrow keys arrive as a three-byte CSI sequence (ESC [ A), so reading a byte
// at a time would deliver them as three separate keys — the bare ESC would
// then look like a keypress of its own. Reading into a buffer lets the whole
// sequence be recognised and collapsed into one rune.
func ReadKey(keyCh chan<- rune, done <-chan struct{}) {
	buf := make([]byte, 16)
	for {
		select {
		case <-done:
			return
		default:
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			for _, key := range decodeKeys(buf[:n]) {
				select {
				case keyCh <- key:
				case <-done:
					return
				}
			}
		}
	}
}

// decodeKeys turns a chunk of raw terminal input into keys, translating CSI
// arrow sequences into their private-use runes. A paste or a fast keypress can
// deliver several keys in one read, so it returns a slice.
func decodeKeys(b []byte) []rune {
	var keys []rune
	for i := 0; i < len(b); {
		// ESC [ <final byte>
		if b[i] == 0x1b && i+2 < len(b) && b[i+1] == '[' {
			if key, ok := arrowKey(b[i+2]); ok {
				keys = append(keys, key)
				i += 3
				continue
			}
		}
		keys = append(keys, rune(b[i]))
		i++
	}
	return keys
}

// arrowKey maps the final byte of a CSI sequence to an arrow key.
func arrowKey(final byte) (rune, bool) {
	switch final {
	case 'A':
		return KeyUp, true
	case 'B':
		return KeyDown, true
	case 'C':
		return KeyRight, true
	case 'D':
		return KeyLeft, true
	}
	return 0, false
}
