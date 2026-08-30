//go:build linux

package jump

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// commandTimeout bounds every call out to a compositor. These are local IPC
// round-trips that normally finish in milliseconds; the deadline exists so a
// wedged compositor cannot stall the live view, which calls Focus inline from
// its key handler. It is short on purpose -- unlike the macOS path, nothing
// here can raise a consent dialog the user needs time to answer.
const commandTimeout = 3 * time.Second

// backend is one display server's way of listing and focusing windows.
type backend interface {
	// name is how the backend is described in user-facing messages.
	name() string
	list() ([]window, error)
	focus(id string) error
}

// detectBackend picks the backend for the session csm is running in.
//
// Detection is by environment variable rather than by which tools are
// installed: HYPRLAND_INSTANCE_SIGNATURE means Hyprland is the compositor
// whether or not hyprctl is on PATH, and reporting the missing tool is far
// more useful than silently trying a backend that cannot work.
func detectBackend() (backend, error) {
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		return needsTool(hyprland{}, "hyprctl")
	case os.Getenv("SWAYSOCK") != "":
		return needsTool(sway{}, "swaymsg")
	case os.Getenv("WAYLAND_DISPLAY") != "":
		// Wayland gives no protocol for one client to focus another's window;
		// each compositor exposes its own or none at all. GNOME and KDE are
		// the common cases with none.
		return nil, unsupportedf("jumping needs a compositor csm can drive — Hyprland and sway are supported, this one (%s) has no way for csm to focus a window",
			desktopName())
	case os.Getenv("DISPLAY") != "":
		return needsTool(x11{}, "wmctrl")
	}
	return nil, unsupportedf("no display server found — jumping needs a graphical session")
}

// needsTool returns the backend only if the command it drives is installed.
func needsTool(b backend, tool string) (backend, error) {
	if _, err := exec.LookPath(tool); err != nil {
		return nil, unsupportedf("jumping on %s needs %s, which is not installed", b.name(), tool)
	}
	return b, nil
}

// desktopName reports the desktop environment for error messages, falling back
// to something honest rather than empty.
func desktopName() string {
	return cmp.Or(os.Getenv("XDG_CURRENT_DESKTOP"), "unknown")
}

// run executes a compositor command and returns its stdout.
//
// stdout comes back even when the command failed, because hyprctl reports what
// went wrong there rather than on stderr and exits non-zero for a dispatch it
// could not parse; discarding it would leave nothing to tell a version
// mismatch apart from a real refusal. The error prefers stderr (wmctrl's
// "Cannot open display"), then stdout, and only then the bare exit status,
// which on its own says nothing a user can act on.
func run(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()
	if ctx.Err() == context.DeadlineExceeded {
		// Without this the SIGKILL that enforced the deadline surfaces as
		// "signal: killed", which reads as a crash.
		return out, fmt.Errorf("%s didn't respond in time", name)
	}
	if err != nil {
		if msg := stderrLine(err); msg != "" {
			return out, fmt.Errorf("%s: %s", name, msg)
		}
		if msg := firstLine(strings.TrimSpace(string(out))); msg != "" {
			return out, fmt.Errorf("%s: %s", name, msg)
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// --- Hyprland ---------------------------------------------------------------

type hyprland struct{}

func (hyprland) name() string { return "Hyprland" }

func (hyprland) list() ([]window, error) {
	out, err := run("hyprctl", "-j", "clients")
	if err != nil {
		return nil, err
	}
	return parseHyprctlClients(out)
}

// hyprFocusForms spell "focus this window" for hyprctl, newest first. The
// address is what `hyprctl clients` reported, already 0x-prefixed.
//
// Hyprland 0.56 replaced dispatch arguments with a Lua API: `focuswindow
// address:0x…` is a syntax error there, and `hl.dsp.focus{…}` is not a
// dispatcher anything older recognises. Version-sniffing would have to guess
// where distro patches put the boundary, so the forms are tried in turn.
var hyprFocusForms = []func(addr string) []string{
	func(addr string) []string {
		return []string{"dispatch", fmt.Sprintf("hl.dsp.focus{window=%q}", "address:"+addr)}
	},
	func(addr string) []string {
		return []string{"dispatch", "focuswindow", "address:" + addr}
	},
}

// hyprWorkingForm is the index of the form this Hyprland understood, so the
// form that cannot work here is spawned once per csm run rather than once per
// jump. First answer wins; the compositor does not change version underneath
// a running dashboard.
var hyprWorkingForm atomic.Int32

func (hyprland) focus(id string) error {
	first := int(hyprWorkingForm.Load())
	for i := range hyprFocusForms {
		n := (first + i) % len(hyprFocusForms)
		out, err := run("hyprctl", hyprFocusForms[n](id)...)
		body := strings.TrimSpace(string(out))

		// Only a rejection of the spelling justifies trying the other form.
		// Returning the last failure instead would let the legacy form's
		// syntax complaint overwrite the real verdict from the form that did
		// work, and report a version incompatibility that does not exist.
		if hyprRejectedForm(body) {
			continue
		}
		if err != nil {
			return err
		}

		hyprWorkingForm.Store(int32(n)) //nolint:gosec // index of a 2-element slice

		// hyprctl exits 0 whether the dispatch worked or named a window that
		// is already gone, so the body is the only signal. "ok" is success and
		// nothing else is.
		if strings.HasPrefix(body, "ok") {
			return nil
		}
		return fmt.Errorf("hyprctl: %s", hyprComplaint(body))
	}
	return fmt.Errorf("this Hyprland understood neither way of focusing a window — please report the output of `hyprctl version`")
}

// hyprRejectedForm reports whether hyprctl refused the *spelling* of a dispatch
// rather than answering it.
//
// Measured on 0.56.2: an unparseable dispatch exits 7 and prints a Lua syntax
// error ("')' expected near 'address'") or "hl.dispatch: expected a dispatcher"
// on stdout, while one it understood exits 0 and prints "ok" or a
// "warning: … window not found". Pre-0.56 hyprctl exits 0 for everything and
// prints "Invalid dispatcher" for a name it does not know, so the exit status
// alone cannot carry this decision -- the body has to.
func hyprRejectedForm(body string) bool {
	return strings.Contains(body, "Invalid dispatcher") ||
		strings.Contains(body, "expected a dispatcher") ||
		strings.Contains(body, "expected near")
}

// hyprComplaint reduces a hyprctl refusal to the part worth showing. Measured
// on 0.56.2, a refusal arrives as
//
//	warning: =[C]:-1: hl.focus: window not found
//
// Neither the severity label nor the Lua chunk that raised it survives contact
// with a user: the sentence is already introduced as a failure to focus, and
// the chunk names hyprctl's internals.
func hyprComplaint(body string) string {
	msg := firstLine(body)
	for _, label := range []string{"error: ", "warning: "} {
		msg = strings.TrimPrefix(msg, label)
	}
	// A Lua chunk location: "=[C]:-1: " for a native binding, or
	// "[string \"…\"]:1: " for a compiled snippet.
	if strings.HasPrefix(msg, "=[") || strings.HasPrefix(msg, `[string "`) {
		if i := strings.Index(msg, "]:"); i >= 0 {
			if j := strings.Index(msg[i:], ": "); j >= 0 {
				msg = strings.TrimSpace(msg[i+j+2:])
			}
		}
	}
	return msg
}

// hyprClient is the subset of `hyprctl -j clients` csm reads.
type hyprClient struct {
	Address string `json:"address"`
	PID     int    `json:"pid"`
	Title   string `json:"title"`
}

func parseHyprctlClients(data []byte) ([]window, error) {
	var clients []hyprClient
	if err := json.Unmarshal(data, &clients); err != nil {
		return nil, fmt.Errorf("could not read Hyprland's window list: %w", err)
	}
	wins := make([]window, 0, len(clients))
	for _, c := range clients {
		wins = append(wins, window{ID: c.Address, PID: c.PID, Title: c.Title})
	}
	return wins, nil
}

// --- sway -------------------------------------------------------------------

type sway struct{}

func (sway) name() string { return "sway" }

func (sway) list() ([]window, error) {
	out, err := run("swaymsg", "-t", "get_tree", "-r")
	if err != nil {
		return nil, err
	}
	return parseSwayTree(out)
}

func (sway) focus(id string) error {
	_, err := run("swaymsg", fmt.Sprintf("[con_id=%s] focus", id))
	return err
}

// swayNode is one node of sway's window tree. The tree is recursive, and only
// leaves carry a pid.
type swayNode struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	PID         int        `json:"pid"`
	Nodes       []swayNode `json:"nodes"`
	FloatingCon []swayNode `json:"floating_nodes"`
}

func parseSwayTree(data []byte) ([]window, error) {
	var root swayNode
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("could not read sway's window tree: %w", err)
	}
	var wins []window
	// Floating windows live in a separate list from tiled ones, and a session
	// is just as likely to be in either.
	var walk func(n swayNode)
	walk = func(n swayNode) {
		if n.PID > 0 {
			wins = append(wins, window{ID: strconv.Itoa(n.ID), PID: n.PID, Title: n.Name})
		}
		for _, child := range n.Nodes {
			walk(child)
		}
		for _, child := range n.FloatingCon {
			walk(child)
		}
	}
	walk(root)
	return wins, nil
}

// --- X11 --------------------------------------------------------------------

type x11 struct{}

func (x11) name() string { return "X11" }

func (x11) list() ([]window, error) {
	out, err := run("wmctrl", "-lp")
	if err != nil {
		return nil, err
	}
	return parseWmctrlList(out), nil
}

func (x11) focus(id string) error {
	_, err := run("wmctrl", "-i", "-a", id)
	return err
}

// parseWmctrlList reads `wmctrl -lp` output, whose columns are:
//
//	0x02c00007  0 12345  hostname  Window title with spaces
//
// The title is the fifth column onwards, rejoined with single spaces. Column
// padding varies, so the original run of spaces between words cannot be
// recovered from Fields anyway -- and searching the line for the hostname
// instead would cut at its first occurrence anywhere, which for hosts named
// like a hex digit or a pid substring lands inside the earlier columns.
func parseWmctrlList(data []byte) []window {
	var wins []window
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		wins = append(wins, window{
			ID:    fields[0],
			PID:   pid,
			Title: strings.Join(fields[4:], " "),
		})
	}
	return wins
}
