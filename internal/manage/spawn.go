package manage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// agentCommand is the command launched in each managed agent window. The
// single place that hardcodes "claude" — a future second agent type becomes
// a choice here, not a scattered change.
const agentCommand = "claude"

// Spawn is invoked by the `csm __spawn` hidden subcommand, bound to <prefix>n
// inside the csm tmux session.
//
// Two modes:
//   - no args: open a tmux command-prompt (pre-filled with the launch dir)
//     that re-invokes `csm __spawn <dir>` with the chosen project.
//   - one arg: the chosen project dir — create the worktree and agent window.
func Spawn(args []string) error {
	if err := ensureInCSMSession(); err != nil {
		return err
	}
	if len(args) == 0 {
		return openProjectPrompt()
	}
	return spawnAgent(strings.TrimSpace(args[0]))
}

// ensureInCSMSession guards against the server-global key binding firing in
// some other tmux session. The binding exists server-wide, but the helper
// only acts inside the csm session.
func ensureInCSMSession() error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("`csm __spawn` must run inside the csm tmux session (use `csm manage`)")
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return fmt.Errorf("query tmux session name: %w", err)
	}
	if strings.TrimSpace(string(out)) != sessionName {
		return fmt.Errorf("not in the %q tmux session", sessionName)
	}
	return nil
}

// openProjectPrompt shows a tmux command-prompt for the project directory,
// defaulting to CSM_PROJECT_ROOT, then re-invokes this binary to do the work.
func openProjectPrompt() error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own binary: %w", err)
	}
	def := tmuxSessionEnv("CSM_PROJECT_ROOT")
	// %1 is replaced by tmux with the prompt answer.
	runCmd := fmt.Sprintf("run-shell \"%s __spawn '%%1'\"", bin)
	cmd := exec.Command("tmux", "command-prompt",
		"-p", "Project dir:",
		"-I", def,
		runCmd,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux command-prompt failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func spawnAgent(dir string) error {
	if dir == "" {
		return displayErr("no project directory given")
	}
	if !isGitRepo(dir) {
		return displayErr(fmt.Sprintf("%s is not a git repository", dir))
	}
	repo, err := gitToplevel(dir)
	if err != nil {
		return displayErr(err.Error())
	}
	project := filepath.Base(repo)

	name, err := nextAgentName(project)
	if err != nil {
		return displayErr(fmt.Sprintf("derive agent name: %v", err))
	}
	branch := fmt.Sprintf("csm/%s/%s", project, name)

	worktree, err := worktreePath(project, name)
	if err != nil {
		return displayErr(err.Error())
	}

	if err := worktreeAdd(repo, branch, worktree); err != nil {
		return displayErr(err.Error())
	}

	// Record before opening the window so a crash mid-spawn is recoverable.
	agent := Agent{
		Project:    project,
		Name:       name,
		Repo:       repo,
		Worktree:   worktree,
		Branch:     branch,
		TmuxWindow: name,
		CreatedAt:  time.Now(),
	}
	if err := SaveAgent(agent); err != nil {
		return displayErr(fmt.Sprintf("save agent record: %v", err))
	}

	if err := openAgentWindow(agent); err != nil {
		return displayErr(err.Error())
	}
	return nil
}

func worktreePath(project, name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude-monitor", "worktrees", project, name), nil
}

func openAgentWindow(a Agent) error {
	out, err := exec.Command("tmux", "new-window",
		"-t", sessionName,
		"-n", a.Name,
		"-c", a.Worktree,
		"-e", "CSM_MANAGED=1",
		"-e", "CSM_AGENT="+a.Name,
		agentCommand,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-window failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// tmuxSessionEnv reads a session environment variable set on the csm session,
// returning "" if unset or on error.
func tmuxSessionEnv(key string) string {
	out, err := exec.Command("tmux", "show-environment", "-t", sessionName, key).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	// Format: KEY=value, or "-KEY" when unset.
	if eq := strings.IndexByte(line, '='); eq >= 0 {
		return line[eq+1:]
	}
	return ""
}

// displayErr surfaces an error to the user via tmux and also returns it so
// the process exits non-zero.
func displayErr(msg string) error {
	_ = exec.Command("tmux", "display-message", "csm: "+msg).Run()
	return fmt.Errorf("%s", msg)
}
