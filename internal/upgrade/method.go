package upgrade

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Method is how this csm binary got onto the machine, which decides whether
// upgrading is csm's job or its package manager's.
type Method int

const (
	// MethodDirect is an unmanaged binary — install.sh, a manual download, or
	// `make install`. Nothing else tracks the file, so csm may replace it.
	MethodDirect Method = iota
	MethodHomebrew
	MethodDpkg
	MethodRPM
	MethodPacman
	MethodMise
	MethodGo
)

// Command returns the command that upgrades a managed install, or "" when csm
// can do it itself.
func (m Method) Command() string {
	switch m {
	case MethodHomebrew:
		return "brew upgrade csm"
	case MethodDpkg:
		return "download the new .deb from https://github.com/" + repo + "/releases/latest and run: sudo dpkg -i <file>.deb"
	case MethodRPM:
		return "download the new .rpm from https://github.com/" + repo + "/releases/latest and run: sudo rpm -U <file>.rpm"
	case MethodPacman:
		return "yay -Syu csm-bin   # or your AUR helper of choice"
	case MethodMise:
		return "mise upgrade csm"
	case MethodGo:
		return "go install github.com/" + repo + "@latest"
	default:
		return ""
	}
}

// Name is how the method is described to the user.
func (m Method) Name() string {
	switch m {
	case MethodHomebrew:
		return "Homebrew"
	case MethodDpkg:
		return "a .deb package"
	case MethodRPM:
		return "an .rpm package"
	case MethodPacman:
		return "pacman"
	case MethodMise:
		return "mise"
	case MethodGo:
		return "go install"
	default:
		return "a direct download"
	}
}

// Managed reports whether some other tool owns this binary.
func (m Method) Managed() bool { return m != MethodDirect }

// owns runs a package manager's "which package owns this file" query and
// reports whether it claimed the path. A missing manager is not an error --
// most machines have at most one of these.
func owns(tool string, args ...string) bool {
	if _, err := exec.LookPath(tool); err != nil {
		return false
	}
	// Bounded: these are local database lookups, but rpm in particular can
	// block on a stale lock, and an upgrade check must not hang on it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, tool, args...).Run() == nil
}

// Detect classifies the binary at exePath.
//
// Path checks come first because they are free and unambiguous; the package
// manager queries only run when the path says nothing. probe is the seam tests
// use to stand in for dpkg/rpm/pacman.
func Detect(exePath string, probe func(tool string, args ...string) bool) Method {
	if probe == nil {
		probe = owns
	}

	// Homebrew keeps everything under a Cellar, both on macOS (/opt/homebrew,
	// /usr/local) and on Linux (/home/linuxbrew/.linuxbrew).
	if strings.Contains(exePath, "/Cellar/") || strings.Contains(exePath, "/linuxbrew/") {
		return MethodHomebrew
	}
	if strings.Contains(exePath, "/mise/installs/") {
		return MethodMise
	}
	if inGoBin(exePath) {
		return MethodGo
	}

	switch {
	case probe("pacman", "-Qo", exePath):
		return MethodPacman
	case probe("dpkg", "-S", exePath):
		return MethodDpkg
	case probe("rpm", "-qf", exePath):
		return MethodRPM
	}

	return MethodDirect
}

// inGoBin reports whether the path is where `go install` puts binaries.
// GOBIN wins when set, otherwise GOPATH/bin, otherwise the ~/go/bin default.
func inGoBin(exePath string) bool {
	dir := filepath.Dir(exePath)
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		return dir == filepath.Clean(gobin)
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		for _, p := range filepath.SplitList(gopath) {
			if dir == filepath.Join(p, "bin") {
				return true
			}
		}
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return dir == filepath.Join(home, "go", "bin")
}
