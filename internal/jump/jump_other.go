//go:build !darwin

package jump

import (
	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// errNotDarwin is one value rather than a fresh error per call. A stub that
// allocates a new error each time lets a caller's `err != nil` be proved always
// true, and the success branch behind it is then reported as dead code (SA4023)
// on every non-darwin build.
var errNotDarwin = unsupportedf("jumping is macOS-only for now")

// Focus is macOS-only for now. Linux window focus is display-server specific
// (wmctrl/xdotool on X11, compositor-gated on Wayland) and needs its own design.
func Focus(session.Session) (Result, error) {
	return Result{}, errNotDarwin
}
