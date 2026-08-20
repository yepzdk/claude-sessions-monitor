// Package jump brings the terminal window/tab hosting a Claude session to the
// foreground. Only the OS-level focus changes; csm stays where it is.
package jump

import (
	"errors"
	"fmt"
)

// ErrUnsupported reports that we have no way to focus this session's terminal.
// Callers surface the message to the user rather than treating it as a failure.
var ErrUnsupported = errors.New("no way to focus this terminal")

// Result describes what a focus attempt did, so the UI can say something more
// useful than "ok"/"failed".
type Result struct {
	Matches int    // number of candidate terminals that matched
	Name    string // title of the terminal we focused
	Exact   bool   // true when matched by tty (unambiguous), false when by working directory
}

// Message renders the result as a single line of user-facing feedback.
func (r Result) Message() string {
	switch {
	case r.Matches > 1:
		return fmt.Sprintf("%d tabs matched — focused best guess", r.Matches)
	case r.Name != "":
		return "Focused " + r.Name
	default:
		return "Focused session"
	}
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
