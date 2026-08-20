//go:build darwin

package jump

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// osascriptTimeout bounds the AppleScript call. Unlike the ps/lsof calls
// elsewhere in csm, this one can block on a human: the first jump triggers
// macOS's Automation consent dialog, which waits indefinitely for an answer.
// It runs on the render loop's goroutine, so a hang would freeze the UI.
const osascriptTimeout = 3 * time.Second

// listGhosttyTerminals asks Ghostty for every open terminal surface.
//
// The tty property only exists from the Ghostty release containing
// ghostty-org/ghostty#11922; on older builds reading it raises error -1700, so
// each read is wrapped in its own try block and yields "" instead of aborting
// the whole listing. That's what lets pick() upgrade from directory matching to
// exact tty matching with no version check and no config flag.
const listGhosttyTerminals = `
tell application "Ghostty"
	set sep to (ASCII character 9)
	set out to ""
	repeat with t in terminals
		set theTTY to ""
		try
			set theTTY to (tty of t) as text
		end try
		set theDir to ""
		try
			set theDir to (working directory of t) as text
		end try
		set theName to ""
		try
			set theName to (name of t) as text
		end try
		set out to out & (id of t) & sep & theTTY & sep & theDir & sep & theName & linefeed
	end repeat
	return out
end tell
`

// focusGhosttyTerminal selects the tab holding the given surface and brings
// Ghostty forward. "focus" alone selects the tab without raising the app, so
// the "activate" is what actually puts it in front of the user.
const focusGhosttyTerminal = `
on run argv
	tell application "Ghostty"
		repeat with t in terminals
			if (id of t) as text is (item 1 of argv) then
				focus t
				activate
				return "OK"
			end if
		end repeat
		return "GONE"
	end tell
end run
`

// Focus brings the terminal hosting s to the foreground.
func Focus(s session.Session) (Result, error) {
	// Ghostty is the only app csm can drive today; see the package doc.
	if s.Origin.App != "ghostty" {
		what := s.Origin.Display
		if what == "" {
			what = "this session's terminal"
		}
		return Result{}, unsupportedf("can't jump to %s yet", what)
	}

	out, err := runOsascript(listGhosttyTerminals)
	if err != nil {
		return Result{}, fmt.Errorf("couldn't reach Ghostty: %w", err)
	}

	chosen, matches, exact := pick(parseTerminals(out), ttyForPID(s.GhostPID), s.CWD)
	if matches == 0 {
		return Result{}, fmt.Errorf("no Ghostty tab open in %s", s.CWD)
	}

	res, err := runOsascript(focusGhosttyTerminal, chosen.ID)
	if err != nil {
		return Result{}, fmt.Errorf("couldn't focus the tab: %w", err)
	}
	if strings.TrimSpace(res) == "GONE" {
		return Result{}, fmt.Errorf("that tab just closed")
	}

	return Result{Matches: matches, Name: chosen.Name, Exact: exact}, nil
}

// parseTerminals reads the tab-separated listing produced by
// listGhosttyTerminals. Tab is safe as a separator because none of the four
// fields can contain one.
func parseTerminals(out string) []candidate {
	var cands []candidate
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 4 || fields[0] == "" {
			continue
		}
		cands = append(cands, candidate{
			ID:   fields[0],
			TTY:  fields[1],
			Dir:  fields[2],
			Name: fields[3],
		})
	}
	return cands
}

// ttyForPID returns the controlling terminal of pid as a device path
// ("/dev/ttys002") to match what Ghostty reports. ps prints the short form
// ("ttys002"), or "??" when the process has no controlling terminal.
func ttyForPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("ps", "-o", "tty=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if name == "" || name == "??" || name == "?" {
		return ""
	}
	return "/dev/" + name
}

// runOsascript feeds a script to osascript on stdin, so no script file has to
// be written to disk and no argument ever reaches a shell.
func runOsascript(script string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), osascriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", append([]string{"-"}, args...)...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("Ghostty didn't respond (permission dialog still open?)")
	}
	if err != nil {
		return "", err
	}
	return string(out), nil
}
