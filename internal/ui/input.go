package ui

import (
	"errors"
	"fmt"
	"os"
	"syscall"

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
		if err := term.Restore(int(os.Stdin.Fd()), originalState); err != nil {
			// The shell is now left with echo off and no line discipline, which
			// looks like a hang. Naming the fix turns that into a one-liner.
			fmt.Fprintf(os.Stderr, "csm: could not restore the terminal (%v); run 'stty sane'\n", err)
		}
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
			if err != nil {
				// EINTR is worth retrying. EOF is not: stdin is gone for good
				// when csm is started detached or its pty is destroyed, and
				// retrying it spins this goroutine at full speed forever while
				// the display keeps refreshing and looks perfectly healthy.
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				return
			}
			if n == 0 {
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

// decodeKeys turns a chunk of raw terminal input into keys, translating arrow
// sequences into their private-use runes. A paste or a fast keypress can
// deliver several keys in one read, so it returns a slice.
//
// Arrows arrive two ways: as CSI (ESC [ A) normally, and as SS3 (ESC O A) when
// the terminal is in application-cursor-key mode, which csm can inherit from
// whatever ran before it. Both are handled. Any other escape sequence is
// dropped rather than re-emitted byte by byte — otherwise its trailing letter
// lands in the key switch as a command, so e.g. Home (ESC O H) would silently
// switch to the history view.
func decodeKeys(b []byte) []rune {
	var keys []rune
	for i := 0; i < len(b); {
		if b[i] == 0x1b && i+1 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
			n, key, ok := escapeSequence(b[i:])
			if ok {
				keys = append(keys, key)
			}
			i += n
			continue
		}
		keys = append(keys, rune(b[i]))
		i++
	}
	return keys
}

// escapeSequence consumes one ESC-introduced sequence from the front of b,
// returning its length and, when it's a key we act on, the key. A sequence runs
// to its first final byte (@ through ~), so unknown ones are consumed whole
// instead of leaking their bytes into the key stream.
func escapeSequence(b []byte) (n int, key rune, ok bool) {
	for i := 2; i < len(b); i++ {
		if b[i] >= '@' && b[i] <= '~' {
			key, ok = arrowKey(b[i])
			return i + 1, key, ok
		}
	}
	return len(b), 0, false // truncated; consume what we have
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
