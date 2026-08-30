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

// ompTerminalID converts a controlling terminal as ps reports it into the id omp
// builds its breadcrumb filename from.
//
// omp takes ttyname(3) and drops the `/dev/` prefix, then replaces the remaining
// separators because a filename cannot hold them:
//
//	if (a?.startsWith("/dev/")) return a.slice(5).replace(/\//g, "-")
//
// So Linux `/dev/pts/3`, which `ps -o tty=` prints as `pts/3`, is `pts-3` on
// disk. macOS `/dev/ttys003` survives unchanged, which is why comparing the two
// byte-for-byte passed every test on this machine and would still have failed
// for every session on the Linux binaries the release workflow ships: no jump,
// and --kill-ghosts silently skipping every omp ghost forever.
func ompTerminalID(tty string) string {
	return strings.ReplaceAll(tty, "/", "-")
}

// ompBreadcrumbs is omp's terminal <-> session mapping, in both directions.
// Pairing needs both: which terminal claims this log, and whether some *other*
// log's terminal already accounts for a process.
type ompBreadcrumbs struct {
	// terminalOf maps a session's log path to the terminal claiming it, or
	// ompAmbiguousTerminal when more than one does.
	terminalOf map[string]string
	// logOf maps a terminal id to the log it claims.
	logOf map[string]string
}

// ompTerminalSessions reads the breadcrumb directory. A missing or unreadable
// one yields empty maps and, with them, unconfident pairings; it is not an error
// worth failing a sweep over.
func ompTerminalSessions() ompBreadcrumbs {
	crumbs := ompBreadcrumbs{terminalOf: map[string]string{}, logOf: map[string]string{}}

	dir, err := ompTerminalSessionsDir()
	if err != nil {
		return crumbs
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return crumbs
	}

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

		crumbs.logOf[entry.Name()] = logPath
		if _, seen := crumbs.terminalOf[logPath]; seen {
			crumbs.terminalOf[logPath] = ompAmbiguousTerminal
			continue
		}
		crumbs.terminalOf[logPath] = entry.Name()
	}
	return crumbs
}

// pairOMPProcess decides, for one log file, whether its session is running,
// which pid is its own, and whether that pid is certain.
//
//   - The log's breadcrumb names a terminal and one of the directory's omp
//     processes is attached to it: that process, confident.
//   - Every process in the directory is claimed by a *different* log's
//     breadcrumb: not running. csm has exact evidence those processes belong
//     elsewhere, so the generous fallback below would keep an exited session in
//     the active list until its log aged out of the freshness window. This
//     mirrors pairProcess's "registry present and every pid accounted for ->
//     not running".
//   - Otherwise, some process is running here and none can be tied to this log:
//     running, no pid. Deliberately generous, exactly as the Claude Code path
//     is -- a wrong "running" self-corrects to Waiting through content
//     staleness, whereas a wrong "inactive" hides a live session entirely.
//   - No omp process in the directory at all: not running.
func pairOMPProcess(logFile string, pids []int, crumbs ompBreadcrumbs,
	procByPID map[int]harnessProcess) (isRunning bool, pid int, confident bool) {
	if len(pids) == 0 {
		return false, 0, false
	}

	mine := crumbs.terminalOf[logFile]
	claimedElsewhere := 0
	for _, candidate := range pids {
		terminal := ompTerminalID(procByPID[candidate].tty)
		if terminal != ompAmbiguousTerminal && terminal == mine {
			return true, candidate, true
		}
		if claimed, ok := crumbs.logOf[terminal]; ok && claimed != logFile {
			claimedElsewhere++
		}
	}

	if claimedElsewhere == len(pids) {
		return false, 0, false
	}

	return true, 0, false
}
