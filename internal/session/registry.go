package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// processAlive reports whether pid exists. EPERM still means "exists".
var processAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// readSessionRegistry returns session id -> entry for every registry file whose
// process is still alive. The second result is false when there is no registry
// directory at all (an older Claude Code), so callers can fall back to the
// positional pairing instead of treating every session as stopped.
func readSessionRegistry() (map[string]registryEntry, bool) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	out := make(map[string]registryEntry)
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
		if !processAlive(re.PID) {
			continue
		}
		out[re.SessionID] = re
	}
	return out, true
}

// registryLogsForDir returns the log paths of registry sessions that live in
// projectDir (matched on the encoded cwd) and whose log exists on disk. Used to
// make sure a live-but-quiet session is listed even when fresher logs from
// exited sessions would have pushed it out of the positional top-N.
func registryLogsForDir(registry map[string]registryEntry, encodedName, projectDir string) []string {
	var logs []string
	for id, re := range registry {
		if encodeProjectPath(re.Cwd) != encodedName {
			continue
		}
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
//   - Registry hit: the session's own pid, confident.
//   - Registry present and every ps-found pid for this directory is accounted
//     for by the registry: this session has no process; not running.
//   - Registry present but some pid in this directory is unknown to it: keep
//     the old bias of treating the session as running, but carry no pid.
//   - No registry: the original positional pairing, unchanged.
func pairProcess(sessionID string, registry map[string]registryEntry, haveRegistry bool,
	pids []int, i, logCount int) (isRunning bool, pid int, confident bool) {

	if !haveRegistry {
		isRunning = len(pids) > 0
		if i < len(pids) {
			pid = pids[i]
		}
		confident = len(pids) == 1 && logCount == 1
		return
	}

	if re, ok := registry[sessionID]; ok {
		return true, re.PID, true
	}

	known := make(map[int]bool, len(registry))
	for _, re := range registry {
		known[re.PID] = true
	}
	for _, p := range pids {
		if !known[p] {
			// A claude process this registry cannot name -- an older
			// build, or one whose registration failed. Do not demote the
			// session on that evidence, but do not guess its pid either.
			return true, 0, false
		}
	}
	return false, 0, false
}
