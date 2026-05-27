package manage

import (
	"fmt"
	"os/exec"
	"strings"
)

// isGitRepo reports whether dir is inside a git working tree.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

// gitToplevel returns the absolute path to the root of the working tree
// containing dir.
func gitToplevel(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel in %q: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// worktreeAdd creates a new worktree at path on a fresh branch off HEAD.
// Fails (without partial state) if the branch already exists or path is taken.
func worktreeAdd(repo, branch, path string) error {
	out, err := exec.Command("git", "-C", repo,
		"worktree", "add", "-b", branch, path, "HEAD",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
