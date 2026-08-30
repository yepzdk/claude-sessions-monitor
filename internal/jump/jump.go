// Package jump brings the terminal window/tab hosting a Claude session to the
// foreground. Only the OS-level focus changes; csm stays where it is.
package jump

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrUnsupported reports that we have no way to focus this session's terminal.
// Callers surface the message to the user rather than treating it as a failure.
var ErrUnsupported = errors.New("no way to focus this terminal")

// Result describes what a focus attempt did, so the UI can say something more
// useful than "ok"/"failed".
type Result struct {
	// Matches is the number of candidates the chosen one was chosen from.
	// More than one means the choice was a guess.
	Matches int
	// Noun names what a candidate is on this platform, singular: macOS drives
	// Ghostty's tabs, Linux the compositor's windows. The shared message would
	// otherwise have to hard-code one platform's vocabulary and be wrong on
	// the other.
	Noun string
	// Name is the title of what was focused.
	Name string
	// Guessed reports that the session was paired to its process positionally
	// rather than certainly, so even a single match may be a sibling session's
	// terminal. The candidate count cannot carry this: the window search that
	// followed can be exact while the pid it started from was not.
	Guessed bool
}

// Message renders the result as a single line of user-facing feedback. A guess
// always names what was focused, because that title is the only way the user
// can see the pick was wrong.
func (r Result) Message() string {
	what := r.Name
	if what == "" {
		what = "session"
	}
	switch {
	case r.Matches > 1:
		return fmt.Sprintf("%d %ss matched — focused best guess: %s", r.Matches, r.noun(), what)
	case r.Guessed:
		return fmt.Sprintf("Focused %s — best guess, csm can't be sure which %s is this session's", what, r.noun())
	default:
		return "Focused " + what
	}
}

func (r Result) noun() string {
	if r.Noun == "" {
		return "terminal"
	}
	return r.Noun
}

// stderrLine returns the first line of a failed command's stderr, or "" when
// the failure carried none. cmd.Output() captures stderr, but ExitError.Error()
// reports only the exit code, so this is the sole route from a command's real
// complaint -- a refused permission, a socket nothing is listening on -- to the
// user.
func stderrLine(err error) string {
	var exit *exec.ExitError
	if !errors.As(err, &exit) || len(exit.Stderr) == 0 {
		return ""
	}
	return firstLine(strings.TrimSpace(string(exit.Stderr)))
}

// firstLine keeps a multi-line complaint to the part that fits on the
// dashboard's one line of feedback.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// unsupportedf builds an ErrUnsupported whose message is the given sentence
// alone. Wrapping with %w would append the sentinel's own text, which reads as
// a redundant second clause once the caller prints it.
func unsupportedf(format string, args ...any) error {
	return unsupportedError{msg: fmt.Sprintf(format, args...)}
}

type unsupportedError struct{ msg string }

func (e unsupportedError) Error() string { return e.msg }
func (e unsupportedError) Unwrap() error { return ErrUnsupported }
