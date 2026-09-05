package upgrade

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// managedBinary writes a stand-in csm at a path that identifies it as owned by
// a package manager, and returns the path plus its original contents.
func managedBinary(t *testing.T, rel string) (path, original string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original = csmLikeScript("v0.1.0")
	if err := os.WriteFile(path, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, original
}

// alwaysYes stands in for a user who typed y, or for -y.
func alwaysYes(io.Writer, string) bool { return true }

// neverAsked fails the test if it is called: the managed and already-latest
// paths change nothing, and a prompt there only teaches a reflexive y.
func neverAsked(t *testing.T) confirmer {
	t.Helper()
	return func(_ io.Writer, question string) bool {
		t.Errorf("csm asked %q on a path that touches nothing", question)
		return false
	}
}

// The property the whole feature rests on: when another tool owns the binary,
// csm names that tool's command and changes nothing. Overwriting a file dpkg,
// rpm, pacman, Homebrew or mise has recorded leaves its database lying, and the
// user is silently reverted on their next system upgrade.
func TestRunRefusesToTouchManagedInstalls(t *testing.T) {
	tests := []struct {
		name    string
		relPath string
		want    string
	}{
		{"homebrew", "Cellar/csm/0.6.0/bin/csm", "brew upgrade csm"},
		{"mise", "mise/installs/csm/0.6.0/bin/csm", "mise upgrade csm"},
		{"linuxbrew", "linuxbrew/bin/csm", "brew upgrade csm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A newer release exists, so the only thing stopping an overwrite
			// is the ownership check.
			fakeAPI(t, Release{TagName: "v9.9.9"})
			t.Setenv("GOBIN", "")
			t.Setenv("GOPATH", "/nonexistent-gopath")

			path, original := managedBinary(t, tt.relPath)

			var out strings.Builder
			if code := runFor("v0.1.0", path, &out, neverAsked(t)); code != 0 {
				t.Errorf("runFor() = %d, want 0 — a managed install is a normal outcome, not a failure", code)
			}

			if got := out.String(); !strings.Contains(got, tt.want) {
				t.Errorf("output does not tell the user to run %q:\n%s", tt.want, got)
			}
			if !strings.Contains(out.String(), "v9.9.9") {
				t.Errorf("output does not name the available version:\n%s", out.String())
			}

			// The point of the whole branch.
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != original {
				t.Errorf("the managed binary was modified:\n%s", got)
			}
		})
	}
}

func TestRunSaysNothingToDoWhenCurrent(t *testing.T) {
	fakeAPI(t, Release{TagName: "v0.6.0"})
	path, original := managedBinary(t, "bin/csm")

	var out strings.Builder
	if code := runFor("v0.6.0", path, &out, neverAsked(t)); code != 0 {
		t.Errorf("runFor() = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "already the latest") {
		t.Errorf("output = %q, want it to say the build is current", out.String())
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Error("an up-to-date csm was rewritten")
	}
}

// A build stamped by `git describe` is ahead of its tag, so it must not be
// replaced by that tag -- the case the version ordering exists to protect.
func TestRunLeavesDevBuildsAheadOfTheirTagAlone(t *testing.T) {
	fakeAPI(t, Release{TagName: "v0.6.0"})
	path, original := managedBinary(t, "bin/csm")

	var out strings.Builder
	if code := runFor("v0.6.0-11-g660745b-dirty", path, &out, neverAsked(t)); code != 0 {
		t.Errorf("runFor() = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "already the latest") {
		t.Errorf("output = %q, want it to leave a build ahead of the tag alone", out.String())
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Error("a build ahead of the latest tag was replaced by it")
	}
}

// An unmanaged binary is csm's to replace -- the other half of the property.
func TestRunUpgradesDirectInstalls(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/nonexistent-gopath")

	_, rel := fakeRelease(t, csmLikeScript("v9.9.9"))
	fakeAPI(t, rel)

	path, original := managedBinary(t, "bin/csm") // .../bin/csm: nothing owns it

	var out strings.Builder
	if code := runFor("v0.1.0", path, &out, alwaysYes); code != 0 {
		t.Fatalf("runFor() = %d, want 0:\n%s", code, out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == original {
		t.Errorf("a direct install was not upgraded:\n%s", out.String())
	}
	if !strings.Contains(string(got), "v9.9.9") {
		t.Errorf("installed binary is not the new release: %q", got)
	}
	// The path being overwritten is the one fact a user needs to answer the
	// prompt, and nothing on screen used to say it.
	if !strings.Contains(out.String(), path) {
		t.Errorf("output does not name the binary it replaced:\n%s", out.String())
	}
}

// Declining is the whole point of the prompt: the binary stays as it was, and
// saying no is not an error.
func TestRunLeavesTheBinaryAloneWhenDeclined(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/nonexistent-gopath")

	_, rel := fakeRelease(t, csmLikeScript("v9.9.9"))
	fakeAPI(t, rel)

	path, original := managedBinary(t, "bin/csm")

	var out strings.Builder
	asked := ""
	decline := func(_ io.Writer, question string) bool {
		asked = question
		return false
	}
	if code := runFor("v0.1.0", path, &out, decline); code != 0 {
		t.Errorf("runFor() = %d, want 0 — declining is not a failure", code)
	}
	if asked == "" {
		t.Error("an unmanaged upgrade replaced the binary without asking")
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("a declined upgrade replaced the binary:\n%s", got)
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("the question did not name the binary at stake:\n%s", out.String())
	}
	// Nothing was downloaded, so nothing is left behind to clean up.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("a declined upgrade left files behind: %v", entries)
	}
}

func TestRunReportsAnUnreachableAPI(t *testing.T) {
	old := apiBase
	apiBase = "http://127.0.0.1:1" // nothing listens here
	t.Cleanup(func() { apiBase = old })

	path, original := managedBinary(t, "bin/csm")

	var out strings.Builder
	if code := runFor("v0.1.0", path, &out, neverAsked(t)); code != 1 {
		t.Errorf("runFor() = %d, want 1 — an explicit upgrade that could not check must fail", code)
	}
	if !strings.Contains(out.String(), "Could not check for updates") {
		t.Errorf("output = %q", out.String())
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Error("a failed check modified the binary")
	}
}
