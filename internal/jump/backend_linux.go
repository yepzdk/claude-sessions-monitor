//go:build linux

package jump

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// commandTimeout bounds every call out to a compositor. These are local IPC
// round-trips that normally finish in milliseconds; the deadline exists so a
// wedged compositor cannot freeze the render loop, which calls Focus on its
// own goroutine. It is short on purpose -- unlike the macOS path, nothing here
// can raise a consent dialog the user needs time to answer.
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
	if d := os.Getenv("XDG_CURRENT_DESKTOP"); d != "" {
		return d
	}
	return "unknown"
}

// run executes a compositor command and returns its stdout.
func run(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
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

func (hyprland) focus(id string) error {
	// Hyprland 0.56 replaced dispatch arguments with a Lua API: the old
	// `focuswindow address:0x…` is a syntax error there, and the new
	// `hl.dsp.focus{window=…}` is an unknown dispatcher on anything older.
	// Version-sniffing would have to guess where distro patches put the
	// boundary, so both forms are tried and the one that answers takes effect.
	// The address is what `hyprctl clients` reported, already 0x-prefixed.
	forms := [][]string{
		{"dispatch", fmt.Sprintf("hl.dsp.focus{window=%q}", "address:"+id)},
		{"dispatch", "focuswindow", "address:" + id},
	}

	var failure error
	for _, args := range forms {
		out, err := run("hyprctl", args...)
		if err != nil {
			failure = err
			continue
		}
		// hyprctl exits 0 whether the dispatch worked, named a window that is
		// gone, or was not understood at all, so the body is the only signal.
		// "ok" is success and nothing else is.
		body := strings.TrimSpace(string(out))
		if strings.HasPrefix(body, "ok") {
			return nil
		}
		failure = fmt.Errorf("hyprctl: %s", firstLine(body))
	}
	return failure
}

// firstLine keeps a compositor's multi-line complaint to the part that fits on
// the dashboard's one line of feedback.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// hyprClient is the subset of `hyprctl -j clients` csm reads.
type hyprClient struct {
	Address string `json:"address"`
	PID     int    `json:"pid"`
	Title   string `json:"title"`
	Class   string `json:"class"`
}

func parseHyprctlClients(data []byte) ([]window, error) {
	var clients []hyprClient
	if err := json.Unmarshal(data, &clients); err != nil {
		return nil, fmt.Errorf("could not read Hyprland's window list: %w", err)
	}
	wins := make([]window, 0, len(clients))
	for _, c := range clients {
		wins = append(wins, window{ID: c.Address, PID: c.PID, Title: c.Title, Class: c.Class})
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
	AppID       string     `json:"app_id"`
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
			wins = append(wins, window{ID: strconv.Itoa(n.ID), PID: n.PID, Title: n.Name, Class: n.AppID})
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
// The title is everything after the hostname, so the split is bounded at five
// fields rather than splitting the whole line.
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
		title := ""
		if parts := strings.SplitN(line, fields[3], 2); len(parts) == 2 {
			title = strings.TrimSpace(parts[1])
		}
		wins = append(wins, window{ID: fields[0], PID: pid, Title: title})
	}
	return wins
}
