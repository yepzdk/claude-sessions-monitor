package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code keeps a per-process registry at ~/.claude/sessions/<pid>.json
// (observed from 2.1.x). Each file carries the process id, the session id and
// the working directory, is written when the session starts and is removed
// when it exits. That makes it two things this package otherwise has to guess
// at: an exact pid <-> session mapping, and a per-session liveness signal.
//
// Without it, a directory holding several sessions is paired positionally
// (newest log <-> first ps result), which the Discover loop comments already
// describe as having no real correspondence.
//
// What the registry is *not* is evidence that a process is alive. A file is
// only removed on a clean exit, so a crash, a SIGKILL or a reboot leaves one
// behind, and the pid it names is eventually handed to some unrelated process.
// Every entry here is therefore checked against the set of live `claude`
// processes ps found before anything is inferred from it.

type registryEntry struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

// sessionsDir locates the registry. A var so tests can point it elsewhere.
var sessionsDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "sessions"), nil
}

// claudePIDSet reduces the process scan to the flat set of `claude` processes ps
// found. The registry is Claude Code's own file, so a pid from another harness
// must never be validated against it: pids are unique per machine, not per
// harness, and a stale registry entry naming a number now held by an omp
// process would resurrect a dead Claude session as running.
func claudePIDSet(procs []harnessProcess) map[int]bool {
	set := make(map[int]bool)
	for _, p := range procs {
		if p.harness == HarnessClaude {
			set[p.pid] = true
		}
	}
	return set
}

// readSessionRegistry returns session id -> entry for every registry file that
// names one of the live claude processes in claudePIDs. The second result is
// false when there is no registry directory at all (an older Claude Code), so
// callers can fall back to the positional pairing instead of treating every
// session as stopped.
//
// claudePIDs is the only liveness test. Asking the kernel whether the pid
// exists is not enough: a stale file's pid is very likely to have been reused
// by then, which would resurrect a dead session as running and point csm's
// tty lookup, origin detection and --kill-ghosts at a stranger's process.
// Claude Code does not trust the bare pid either -- its files carry procStart
// and pidDomain for exactly this reason.
func readSessionRegistry(claudePIDs map[int]bool) (map[string]registryEntry, bool) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	out := make(map[string]registryEntry)
	var ambiguous []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var re registryEntry
		if json.Unmarshal(data, &re) != nil || re.PID <= 0 || re.SessionID == "" {
			continue
		}
		if !claudePIDs[re.PID] {
			continue
		}
		if prev, ok := out[re.SessionID]; ok && prev.PID != re.PID {
			// Two live processes carrying one session id -- `claude --resume S`
			// open in two tabs. Nothing on disk says which of them owns a given
			// log, so keeping whichever file happened to be read last would
			// hand out a pid that is wrong half the time: the very guess this
			// registry exists to replace. Drop the id instead, and let the
			// session fall through to the unknown-process branch of
			// pairProcess, which keeps it listed as running with no pid.
			ambiguous = append(ambiguous, re.SessionID)
			continue
		}
		out[re.SessionID] = re
	}
	for _, id := range ambiguous {
		delete(out, id)
	}
	return out, true
}

// registryLogsForDir returns the log paths of registry sessions whose transcript
// lives in projectDir. Used to make sure a live-but-quiet session is listed even
// when fresher logs from exited sessions would have pushed it out of the
// positional top-N.
//
// Sessions are matched by transcript path rather than by the entry's cwd because
// the two are not the same fact: EnterWorktree chdirs the process and patches
// the cwd in the registry file, and csm cares about where the log is.
//
// The os.Stat stays. parseSession does stat the same path, but only after
// LoadOrigin has read (and DetectOrigin can write) the origin cache, so letting
// a path that does not exist through turns one cheap stat per registry entry
// per project directory into a file read per registry entry per project
// directory, every tick.
func registryLogsForDir(registry map[string]registryEntry, projectDir string) []string {
	var logs []string
	for id := range registry {
		p := filepath.Join(projectDir, id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			logs = append(logs, p)
		}
	}
	return logs
}

// pairProcess decides, for one log file in one project directory, whether the
// session is running, which pid is its own, and whether that pid is certain.
//
//   - Registry hit, for a session whose cwd is this directory: the session's
//     own pid, confident.
//   - Registry present and every ps-found pid for this directory is accounted
//     for by the registry: this session has no process; not running.
//   - Registry present but some pid in this directory is unknown to it: keep
//     the old bias of treating the session as running, but carry no pid.
//   - No registry: the original positional pairing, unchanged.
//
// Every entry in registry has already been checked against the ps-derived set
// of live claude processes by readSessionRegistry, so a hit is a real process.
func pairProcess(sessionID, encodedName string, registry map[string]registryEntry, haveRegistry bool,
	pids []int, i, logCount int) (isRunning bool, pid int, confident bool) {

	if !haveRegistry {
		isRunning = len(pids) > 0
		if i < len(pids) {
			pid = pids[i]
		}
		confident = len(pids) == 1 && logCount == 1
		return
	}

	// The cwd check keeps a log that merely shares a session id with a live
	// session -- a copy under some other project directory -- from reading as
	// that session's running process.
	if re, ok := registry[sessionID]; ok && encodeProjectPath(re.Cwd) == encodedName {
		return true, re.PID, true
	}

	known := make(map[int]bool, len(registry))
	for _, re := range registry {
		known[re.PID] = true
	}
	for _, p := range pids {
		if !known[p] {
			// A claude process this registry cannot name -- an older
			// build, one whose registration failed, or one of a pair
			// sharing a session id. Do not demote the session on that
			// evidence, but do not guess its pid either.
			return true, 0, false
		}
	}
	return false, 0, false
}
