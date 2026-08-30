package upgrade

import (
	"path/filepath"
	"testing"
)

// noProbe stands in for the package-manager queries: nothing owns the file.
func noProbe(string, ...string) bool { return false }

func TestDetectFromPath(t *testing.T) {
	// Isolate from the developer's real Go environment, which would otherwise
	// decide whether ~/go/bin counts.
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/nonexistent-gopath")

	tests := []struct {
		name string
		path string
		want Method
	}{
		{"homebrew on apple silicon", "/opt/homebrew/Cellar/csm/0.6.0/bin/csm", MethodHomebrew},
		{"homebrew on intel macs", "/usr/local/Cellar/csm/0.6.0/bin/csm", MethodHomebrew},
		{"homebrew on linux", "/home/linuxbrew/.linuxbrew/bin/csm", MethodHomebrew},
		{"mise", "/home/u/.local/share/mise/installs/csm/0.6.0/bin/csm", MethodMise},
		{"install.sh default", "/home/u/.local/bin/csm", MethodDirect},
		{"manual /usr/local/bin", "/usr/local/bin/csm", MethodDirect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(tt.path, noProbe); got != tt.want {
				t.Errorf("Detect(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDetectGoBin(t *testing.T) {
	t.Setenv("GOBIN", "/custom/gobin")
	if got := Detect("/custom/gobin/claude-sessions-monitor", noProbe); got != MethodGo {
		t.Errorf("with GOBIN set, Detect() = %v, want MethodGo", got)
	}
	if got := Detect("/somewhere/else/csm", noProbe); got != MethodDirect {
		t.Errorf("outside GOBIN, Detect() = %v, want MethodDirect", got)
	}

	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", filepath.Join("/home", "u", "go"))
	if got := Detect("/home/u/go/bin/claude-sessions-monitor", noProbe); got != MethodGo {
		t.Errorf("with GOPATH set, Detect() = %v, want MethodGo", got)
	}
}

func TestDetectFromPackageManager(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/nonexistent-gopath")

	// The path says nothing about /usr/bin/csm -- only the package database
	// does, which is the case every distro package lands in.
	claims := func(want string) func(string, ...string) bool {
		return func(tool string, _ ...string) bool { return tool == want }
	}

	tests := []struct {
		tool string
		want Method
	}{
		{"pacman", MethodPacman},
		{"dpkg", MethodDpkg},
		{"rpm", MethodRPM},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			if got := Detect("/usr/bin/csm", claims(tt.tool)); got != tt.want {
				t.Errorf("with %s claiming the file, Detect() = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}

// Every managed method must name a command, and the unmanaged one must not:
// Run prints Command() as the user's next step, so an empty string there is a
// dead end on screen.
func TestManagedMethodsHaveCommands(t *testing.T) {
	managed := []Method{MethodHomebrew, MethodDpkg, MethodRPM, MethodPacman, MethodMise, MethodGo}
	for _, m := range managed {
		if !m.Managed() {
			t.Errorf("%v: Managed() = false, want true", m)
		}
		if m.Command() == "" {
			t.Errorf("%v: Command() is empty", m)
		}
		if m.Name() == "" {
			t.Errorf("%v: Name() is empty", m)
		}
	}
	if MethodDirect.Managed() {
		t.Error("MethodDirect.Managed() = true, want false")
	}
	if MethodDirect.Command() != "" {
		t.Errorf("MethodDirect.Command() = %q, want empty", MethodDirect.Command())
	}
}
