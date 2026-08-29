package session

// Pairing an omp session to the process running it.
//
// Claude Code writes ~/.claude/sessions/<pid>.json, which names the pid
// outright. omp writes no pid anywhere. What it does write, best-effort, is a
// breadcrumb per terminal:
//
//	~/.omp/agent/terminal-sessions/<terminal-id>
//	  line 1: original working directory
//	  line 2: absolute path of the session's JSONL
//	  line 3: optional "fresh" marker
//
// The terminal id is the tty name when the process has one, so joining
// breadcrumbs to the tty column of the process scan gives an exact
// session <-> pid pairing. When it does not hold -- omp falls back to
// env-derived ids (TMUX_PANE, CMUX_SURFACE_ID, ZELLIJ_PANE_ID, ...) that ps
// cannot see, and a process can have no controlling terminal at all -- the
// pairing degrades to the same generous positional bias Claude Code sessions
// use without a registry, with PIDConfident false. Everything that acts on a
// pid (--kill-ghosts, jump) already refuses an unconfident pairing.
//
// A breadcrumb is never evidence that a process is alive: it survives the
// session that wrote it. Liveness comes only from the process scan.

import (
	"os"
	"path/filepath"
	"strings"
)

// ompTerminalSessionsDir returns the directory holding omp's terminal
// breadcrumbs. It sits beside the sessions directory, so an overridden sessions
// path moves it too.
func ompTerminalSessionsDir() (string, error) {
	sessionsDir, err := OMPSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(sessionsDir), "terminal-sessions"), nil
}

// ompAmbiguousTerminal marks a session file that more than one breadcrumb points
// at. Guessing which terminal owns it would be a coin flip over which process a
// pid-consuming action hits, so the session is left unpaired instead -- the same
// rule pairProcess applies when two logs claim one session id.
const ompAmbiguousTerminal = ""

// ompTerminalSessions maps a session's log path to the terminal id whose
// breadcrumb points at it. A missing or unreadable directory yields an empty map
// and, with it, unconfident pairings; it is not an error worth failing a sweep
// over.
func ompTerminalSessions() map[string]string {
	dir, err := ompTerminalSessionsDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	byLog := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Two short lines; the cap is only there so a corrupt file cannot be
		// read into memory unbounded.
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || len(content) > 64*1024 {
			continue
		}
		lines := strings.Split(string(content), "\n")
		if len(lines) < 2 {
			continue
		}
		logPath := strings.TrimSpace(lines[1])
		if logPath == "" {
			continue
		}
		if _, seen := byLog[logPath]; seen {
			byLog[logPath] = ompAmbiguousTerminal
			continue
		}
		byLog[logPath] = entry.Name()
	}
	return byLog
}

// pairOMPProcess decides, for one log file, whether its session is running,
// which pid is its own, and whether that pid is certain.
//
//   - The log's breadcrumb names a terminal, and one of the directory's omp
//     processes is attached to that terminal: that process, confident.
//   - Some omp process is running in the directory but none can be tied to this
//     log: running, no pid. Deliberately generous, exactly as the Claude Code
//     path is -- a wrong "running" self-corrects to Waiting through content
//     staleness, whereas a wrong "inactive" hides a live session entirely.
//   - No omp process in the directory: not running.
func pairOMPProcess(logFile string, pids []int, breadcrumbs map[string]string,
	procByPID map[int]harnessProcess) (isRunning bool, pid int, confident bool) {
	if len(pids) == 0 {
		return false, 0, false
	}

	if terminal := breadcrumbs[logFile]; terminal != ompAmbiguousTerminal {
		for _, candidate := range pids {
			if procByPID[candidate].tty == terminal {
				return true, candidate, true
			}
		}
	}

	return true, 0, false
}
