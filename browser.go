package main

import (
	"fmt"
	"os/exec"
)

// browserCommand hands a URL to the desktop's default browser. A var so a test
// can supply a command that fails.
var browserCommand = func(url string) *exec.Cmd { return exec.Command(browserOpener, url) }

// openBrowser opens the given URL in the default browser.
func openBrowser(url string) error {
	cmd := browserCommand(url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", browserOpener, err)
	}
	// Reaped in the background so each press does not leave a zombie for the
	// life of the process. The wait cannot be on this path: the opener can stay
	// up until the browser itself exits.
	go func() { _ = cmd.Wait() }()
	return nil
}
