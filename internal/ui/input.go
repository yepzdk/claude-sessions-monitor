package ui

import (
	"os"

	"golang.org/x/term"
)

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

// ReadKey reads a single keypress from stdin (non-blocking with channel)
func ReadKey(keyCh chan<- rune, done <-chan struct{}) {
	buf := make([]byte, 1)
	for {
		select {
		case <-done:
			return
		default:
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			select {
			case keyCh <- rune(buf[0]):
			case <-done:
				return
			}
		}
	}
}
