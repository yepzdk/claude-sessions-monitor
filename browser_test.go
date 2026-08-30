package main

import (
	"errors"
	"io/fs"
	"os/exec"
	"testing"
)

// A browser that will not start used to be swallowed: pressing w did nothing
// and said nothing about why. xdg-utils is missing on a minimal Linux install,
// which is exactly when the user needs to be told.
func TestOpenBrowserReportsACommandThatCannotStart(t *testing.T) {
	original := browserCommand
	t.Cleanup(func() { browserCommand = original })
	browserCommand = func(string) *exec.Cmd {
		return exec.Command("/nonexistent/browser-opener")
	}

	err := openBrowser("http://localhost:9999")
	if err == nil {
		t.Fatal("a browser command that cannot start reported success")
	}
	// The cause has to survive the wrap, so a caller can tell "not installed"
	// from any other start failure.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the error dropped its cause: %v", err)
	}
}
