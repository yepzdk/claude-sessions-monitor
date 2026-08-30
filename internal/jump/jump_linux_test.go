//go:build linux

package jump

import (
	"strings"
	"testing"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

func TestNoWindowErrorOffersTheFixOnlyWhenThereIsOne(t *testing.T) {
	// Ghostty's one-process-per-window behaviour is a launch flag, so naming
	// it is the whole point of the message.
	ghostty := session.Session{Origin: session.Origin{App: "ghostty", Display: "Ghostty"}}
	got := noWindowError(hyprland{}, 4, ghostty).Error()
	if !strings.Contains(got, "single-instance") {
		t.Errorf("noWindowError() = %q, want the flag that fixes it", got)
	}
	if !strings.Contains(got, "Ghostty") || !strings.Contains(got, "4") {
		t.Errorf("noWindowError() = %q, want the terminal named and the count given", got)
	}

	// gnome-terminal-server has no per-window process mode, so telling its
	// users to turn one off is advice they cannot act on.
	gnome := session.Session{Origin: session.Origin{App: "gnome-terminal", Display: "GNOME Terminal"}}
	got = noWindowError(hyprland{}, 2, gnome).Error()
	if strings.Contains(got, "single-instance") {
		t.Errorf("noWindowError() = %q, want no advice about a mode GNOME Terminal does not have", got)
	}
	if !strings.Contains(got, "GNOME Terminal") {
		t.Errorf("noWindowError() = %q, want the terminal named", got)
	}
}

func TestNoWindowErrorWithNoCandidatesAtAll(t *testing.T) {
	// Nothing owned by the chain is a different failure with a different
	// cause: a multiplexer, an SSH session, another login session.
	got := noWindowError(hyprland{}, 0, session.Session{}).Error()
	if !strings.Contains(got, "multiplexer") {
		t.Errorf("noWindowError() = %q, want the causes that produce no window", got)
	}
}

func TestNoWindowErrorWithAnUnknownTerminal(t *testing.T) {
	// An origin csm never resolved must still produce a sentence that reads.
	got := noWindowError(hyprland{}, 3, session.Session{}).Error()
	if !strings.HasPrefix(got, "That terminal runs as one process") {
		t.Errorf("noWindowError() = %q", got)
	}
}
