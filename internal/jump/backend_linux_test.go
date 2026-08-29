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
