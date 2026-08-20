//go:build !darwin

package jump

import (
	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// Focus is macOS-only for now. Linux window focus is display-server specific
// (wmctrl/xdotool on X11, compositor-gated on Wayland) and needs its own design.
func Focus(session.Session) (Result, error) {
	return Result{}, unsupportedf("jumping is macOS-only for now")
}
