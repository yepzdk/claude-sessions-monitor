// Package manage implements the `csm manage` subcommand: a tmux-driven
// workspace for running and switching between Claude agents. Slice 1 lays
// the skeleton — preflight tmux, then attach to or create a session
// running the standard monitor view. Agent spawning, the sidebar, and
// worktrees follow in later slices.
package manage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

// Single shared tmux session per host for now. Per-project sessions are a
// reasonable future direction — change this to a function of cwd/projectID
// and the attach-or-create flow keeps working.
const sessionName = "csm"

// Run is the entry point for `csm manage`. Preflights tmux, then either
// attaches to an existing csm session or creates a fresh one running the
// monitor view.
func Run() error {
	if err := preflightTmux(); err != nil {
		return err
	}
	if sessionExists(sessionName) {
		return attachExisting(sessionName)
	}
	return createAndAttach(sessionName)
}

func preflightTmux() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required for `csm manage` but was not found on PATH.\n%s", installHint())
	}
	if out, err := exec.Command("tmux", "-V").CombinedOutput(); err != nil {
		return fmt.Errorf("tmux is installed but failed to run: %w\noutput: %s", err, string(out))
	}
	return nil
}

func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "Install with: brew install tmux"
	case "linux":
		return "Install with your package manager, e.g.: sudo apt install tmux"
	default:
		return "See https://github.com/tmux/tmux/wiki for installation instructions."
	}
}

func sessionExists(name string) bool {
	err := exec.Command("tmux", "has-session", "-t", name).Run()
	return err == nil
}

func attachExisting(name string) error {
	return execTmux("attach", "-t", name)
}

func createAndAttach(name string) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own binary: %w", err)
	}

	// Create the session detached, with a single pane running the
	// standard monitor view. -x/-y give a baseline size so layout math
	// doesn't operate on a 0x0 client at creation time.
	if out, err := exec.Command("tmux",
		"new-session", "-d",
		"-s", name,
		"-n", "main",
		"-x", "200", "-y", "50",
		bin,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session failed: %w\noutput: %s", err, string(out))
	}

	// Hide the status bar — we don't have the agent-switching UX yet, so
	// it's just noise. Slice 2b re-enables it when the sidebar uses it.
	if out, err := exec.Command("tmux",
		"set-option", "-t", name, "status", "off",
	).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux set-option status off failed: %w\noutput: %s", err, string(out))
	}

	// Remember where `csm manage` was launched so the spawn prompt can
	// default to the current project.
	if cwd, err := os.Getwd(); err == nil {
		_ = exec.Command("tmux", "set-environment", "-t", name, "CSM_PROJECT_ROOT", cwd).Run()
	}

	// Load the csm key bindings (Ctrl-n to spawn an agent). The bindings are
	// server-global by tmux's nature; the helpers they invoke guard on being
	// inside the csm session, so they're inert elsewhere.
	if err := installBindings(name, bin); err != nil {
		return err
	}

	return execTmux("attach", "-t", name)
}

// installBindings writes a generated tmux config with the csm key bindings
// and sources it into the session.
func installBindings(name, bin string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("unable to determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".claude-monitor", "tmux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create tmux config dir: %w", err)
	}
	conf := filepath.Join(dir, "bindings.conf")
	// Root-table binding: plain Ctrl-n (no prefix) spawns an agent.
	content := fmt.Sprintf("bind-key -n C-n run-shell \"%s __spawn\"\n", bin)
	if err := os.WriteFile(conf, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write bindings config: %w", err)
	}
	if out, err := exec.Command("tmux", "source-file", conf).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux source-file failed: %w\noutput: %s", err, string(out))
	}
	return nil
}

// execTmux replaces the current process with `tmux <args...>`. On success
// it does not return; on failure it returns an error.
func execTmux(args ...string) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux disappeared from PATH: %w", err)
	}
	argv := append([]string{"tmux"}, args...)
	if err := syscall.Exec(tmuxPath, argv, os.Environ()); err != nil {
		return fmt.Errorf("exec tmux: %w", err)
	}
	return errors.New("unreachable")
}
