//go:build linux

package jump

import (
	"errors"
	"os"
	"testing"
)

// The fixture is a real `hyprctl -j clients` dump: the field set is large and
// changes between Hyprland releases, so a hand-written sample would not prove
// the decoder survives the real thing.
func TestParseHyprctlClients(t *testing.T) {
	data, err := os.ReadFile("testdata/hyprctl-clients.json")
	if err != nil {
		t.Fatal(err)
	}

	wins, err := parseHyprctlClients(data)
	if err != nil {
		t.Fatalf("parseHyprctlClients() = %v", err)
	}
	if len(wins) == 0 {
		t.Fatal("parsed no windows from the fixture")
	}
	for _, w := range wins {
		if w.ID == "" {
			t.Errorf("window %+v has no address, so it could never be focused", w)
		}
		if w.PID <= 0 {
			t.Errorf("window %+v has no pid, so it could never be matched", w)
		}
	}
}

func TestParseHyprctlClientsRejectsGarbage(t *testing.T) {
	if _, err := parseHyprctlClients([]byte("not json")); err == nil {
		t.Error("parseHyprctlClients() accepted output that is not JSON")
	}
}

func TestParseSwayTree(t *testing.T) {
	// sway nests windows arbitrarily deep and keeps floating windows in their
	// own list, so both have to be walked.
	tree := []byte(`{
		"id": 1, "name": "root", "nodes": [
			{"id": 2, "name": "output", "nodes": [
				{"id": 3, "name": "alacritty", "pid": 4242, "app_id": "Alacritty"}
			],
			"floating_nodes": [
				{"id": 4, "name": "floating term", "pid": 8484, "app_id": "foot"}
			]}
		]
	}`)

	wins, err := parseSwayTree(tree)
	if err != nil {
		t.Fatalf("parseSwayTree() = %v", err)
	}
	if len(wins) != 2 {
		t.Fatalf("parsed %d windows, want 2 (a tiled one and a floating one)", len(wins))
	}
	if wins[0].ID != "3" || wins[0].PID != 4242 {
		t.Errorf("tiled window = %+v", wins[0])
	}
	if wins[1].ID != "4" || wins[1].PID != 8484 {
		t.Errorf("floating window = %+v, want the floating list to be walked too", wins[1])
	}
}

func TestParseWmctrlList(t *testing.T) {
	// Titles contain spaces, so the parser must not simply split the line.
	out := []byte("0x02c00007  0 12345  archbox  Claude — fix the parser\n" +
		"0x03000005  1 67890  archbox  ~/Projects\n" +
		"malformed line\n")

	wins := parseWmctrlList(out)
	if len(wins) != 2 {
		t.Fatalf("parsed %d windows, want 2 (the malformed line dropped)", len(wins))
	}
	if wins[0].ID != "0x02c00007" || wins[0].PID != 12345 {
		t.Errorf("first window = %+v", wins[0])
	}
	if wins[0].Title != "Claude — fix the parser" {
		t.Errorf("title = %q, want the whole title including spaces", wins[0].Title)
	}
	if wins[1].Title != "~/Projects" {
		t.Errorf("second title = %q", wins[1].Title)
	}
}

func TestParseWmctrlListWithAHostnameThatCollides(t *testing.T) {
	// The title used to be cut at the first occurrence of the hostname
	// anywhere in the line. A host named like a hex digit matches inside the
	// window id, and one that is a substring of the pid matches inside the
	// pid, so both produced a garbled title that then went on to be judged by
	// looksLikePath and shown to the user as what was focused.
	out := []byte("0x00dec001  0 4321  dec  vim\n" +
		"0x02c00007  0 12345  1  htop\n" +
		"0x03000005  0 67890  0  ~/Projects\n")

	wins := parseWmctrlList(out)
	if len(wins) != 3 {
		t.Fatalf("parsed %d windows, want 3", len(wins))
	}
	want := []string{"vim", "htop", "~/Projects"}
	for i, w := range want {
		if wins[i].Title != w {
			t.Errorf("title[%d] = %q, want %q", i, wins[i].Title, w)
		}
	}
}

func TestParseWmctrlListWithNoTitle(t *testing.T) {
	// A window with no title still has to yield a matchable pid.
	wins := parseWmctrlList([]byte("0x02c00007  0 12345  archbox\n"))
	if len(wins) != 1 || wins[0].PID != 12345 || wins[0].Title != "" {
		t.Errorf("parseWmctrlList() = %+v, want one pid-carrying window with an empty title", wins)
	}
}

// The strings below were captured from hyprctl 0.56.2 on Arch. Guessing them
// is the whole risk in this code path: hyprctl reports on stdout, and it exits
// 0 for a dispatch that failed for a real reason as readily as for one it
// understood, so only the body separates "wrong spelling, try the other form"
// from "this is your answer".
func TestHyprRejectedForm(t *testing.T) {
	rejected := []string{
		// 0.56.2, legacy `dispatch focuswindow address:0x…`: the Lua parser
		// chokes on the bare argument.
		`error: [string "return hl.dispatch(focuswindow address:0xdead..."]:1: ')' expected near 'address'`,
		// 0.56.2, a dispatch name it has no Lua binding for.
		"error: return hl.dispatch(totallynotadispatcher):1: hl.dispatch: expected a dispatcher (e.g. hl.dsp.window.close())",
		// Pre-0.56, where `hl.dsp.focus{…}` is just an unknown dispatcher name.
		"Invalid dispatcher",
	}
	for _, body := range rejected {
		if !hyprRejectedForm(body) {
			t.Errorf("hyprRejectedForm(%q) = false, want the other form to be tried", body)
		}
	}

	answered := []string{
		"ok",
		// 0.56.2 with a window that closed between listing and focusing: a
		// real verdict, and returning it beats masking it with the legacy
		// form's syntax complaint.
		"warning: =[C]:-1: hl.focus: window not found",
	}
	for _, body := range answered {
		if hyprRejectedForm(body) {
			t.Errorf("hyprRejectedForm(%q) = true, want the answer to be used as-is", body)
		}
	}
}

func TestHyprComplaintKeepsOnlyTheReason(t *testing.T) {
	// Captured from 0.56.2. The sentence this ends up in already says csm
	// could not focus the window, so hyprctl's severity label is a redundant
	// second one and the Lua chunk that raised it names hyprctl's internals.
	got := hyprComplaint("warning: =[C]:-1: hl.focus: window not found\n\n → Note: dispatch in lua is a shorthand")
	if got != "hl.focus: window not found" {
		t.Errorf("hyprComplaint() = %q, want just the reason", got)
	}

	// A refusal with no label or chunk must survive untouched.
	if got := hyprComplaint("no such window"); got != "no such window" {
		t.Errorf("hyprComplaint() = %q, want it returned as-is", got)
	}
}

func TestDetectBackendReportsWhatIsMissing(t *testing.T) {
	// A compositor csm cannot drive must say so, rather than failing later
	// with something about a missing command.
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("SWAYSOCK", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_CURRENT_DESKTOP", "GNOME")

	_, err := detectBackend()
	if err == nil {
		t.Fatal("detectBackend() succeeded under GNOME Wayland, where csm cannot focus windows")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("detectBackend() = %v, want an ErrUnsupported so the UI reports it as a limitation", err)
	}
}

func TestDetectBackendWithNoDisplay(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("SWAYSOCK", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")

	if _, err := detectBackend(); err == nil {
		t.Error("detectBackend() succeeded with no display server at all")
	}
}
