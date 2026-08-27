package session

import (
	"os"
	"path/filepath"
	"testing"
)

// dirA and dirB are the encoded project directory names for two synthetic
// project paths, which is the form Discover passes to pairProcess.
var (
	pathA = "/tmp/proj-a"
	pathB = "/tmp/proj-b"
	dirA  = encodeProjectPath(pathA)
	dirB  = encodeProjectPath(pathB)
)

// Three sessions in one directory. ps order and log order disagree, which is
// the case the positional pairing gets wrong. With a registry every session
// gets its own pid, confidently, regardless of either order.
func TestPairProcessUsesRegistryWhenPresent(t *testing.T) {
	registry := map[string]registryEntry{
		"aaa": {PID: 300, SessionID: "aaa", Cwd: pathA},
		"bbb": {PID: 100, SessionID: "bbb", Cwd: pathA},
		"ccc": {PID: 200, SessionID: "ccc", Cwd: pathA},
	}
	pids := []int{100, 200, 300}          // ps order
	logs := []string{"aaa", "bbb", "ccc"} // newest-first

	for i, id := range logs {
		running, pid, confident := pairProcess(id, dirA, registry, true, pids, i, len(logs))
		if !running || pid != registry[id].PID || !confident {
			t.Errorf("%s: running=%v pid=%d confident=%v; want true/%d/true",
				id, running, pid, confident, registry[id].PID)
		}
	}
}

// A log that shares a session id with a live session but sits under a different
// project directory is not that session's process. Without the cwd check it
// would read as running, and confidently, on a pid belonging to a session in
// another directory entirely.
func TestPairProcessRejectsHitFromAnotherProjectDir(t *testing.T) {
	registry := map[string]registryEntry{
		"aaa": {PID: 100, SessionID: "aaa", Cwd: pathA},
	}

	// Nothing else running in dirB: the copy there is simply not running.
	running, pid, confident := pairProcess("aaa", dirB, registry, true, nil, 0, 1)
	if running || pid != 0 || confident {
		t.Errorf("foreign dir, no pids: running=%v pid=%d confident=%v; want false/0/false",
			running, pid, confident)
	}

	// An unexplained process in dirB keeps the running bias, but still must not
	// borrow the other directory's pid.
	running, pid, confident = pairProcess("aaa", dirB, registry, true, []int{555}, 0, 1)
	if !running || pid != 0 || confident {
		t.Errorf("foreign dir, unknown pid: running=%v pid=%d confident=%v; want true/0/false",
			running, pid, confident)
	}
}

// A session absent from the registry, in a directory where the registry
// accounts for every process, has no process: not running, no pid. Before
// this it inherited a neighbour's pid and was reported as running.
func TestPairProcessExitedSessionIsNotRunning(t *testing.T) {
	registry := map[string]registryEntry{
		"live": {PID: 100, SessionID: "live", Cwd: pathA},
	}
	running, pid, confident := pairProcess("gone", dirA, registry, true, []int{100}, 0, 2)
	if running || pid != 0 || confident {
		t.Errorf("exited session: running=%v pid=%d confident=%v; want false/0/false",
			running, pid, confident)
	}
}

// A process the registry cannot name (older build, failed registration) keeps
// the old bias: still treated as running, but no pid is guessed for it.
func TestPairProcessUnknownProcessStaysRunningWithoutPID(t *testing.T) {
	registry := map[string]registryEntry{
		"live": {PID: 100, SessionID: "live", Cwd: pathA},
	}
	running, pid, confident := pairProcess("other", dirA, registry, true, []int{100, 555}, 1, 2)
	if !running || pid != 0 || confident {
		t.Errorf("unknown process: running=%v pid=%d confident=%v; want true/0/false",
			running, pid, confident)
	}
}

// No registry directory: behaviour is exactly the previous positional pairing.
func TestPairProcessFallsBackToPositional(t *testing.T) {
	pids := []int{100, 200}
	r, p, c := pairProcess("x", dirA, nil, false, pids, 1, 2)
	if !r || p != 200 || c {
		t.Errorf("positional i=1: running=%v pid=%d confident=%v; want true/200/false", r, p, c)
	}
	r, p, c = pairProcess("x", dirA, nil, false, []int{100}, 0, 1)
	if !r || p != 100 || !c {
		t.Errorf("single/single: running=%v pid=%d confident=%v; want true/100/true", r, p, c)
	}
	r, p, c = pairProcess("x", dirA, nil, false, nil, 0, 1)
	if r || p != 0 || c {
		t.Errorf("no pids: running=%v pid=%d confident=%v; want false/0/false", r, p, c)
	}
}

// writeRegistry points sessionsDir at a temp directory holding the given
// <name>: <contents> files, and returns nothing: callers read it back through
// readSessionRegistry.
func writeRegistry(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig := sessionsDir
	t.Cleanup(func() { sessionsDir = orig })
	sessionsDir = func() (string, error) { return dir, nil }
}

func TestReadSessionRegistryFiltersMalformed(t *testing.T) {
	writeRegistry(t, map[string]string{
		"100.json":         `{"pid":100,"sessionId":"good","cwd":"/tmp/proj-a"}`,
		"300.json":         `{"pid":300}`,
		"400.json":         `{"pid":400,"sessionId":"torn","cwd":"/tmp/pro`,
		"100.deadbeef.key": `not json`,
		"500.json":         `{"pid":0,"sessionId":"nopid","cwd":"/tmp/proj-a"}`,
	})

	reg, ok := readSessionRegistry(map[int]bool{100: true, 300: true, 400: true, 500: true})
	if !ok {
		t.Fatal("registry directory exists; ok should be true")
	}
	if len(reg) != 1 || reg["good"].PID != 100 {
		t.Errorf("registry = %+v; want only good->100", reg)
	}
}

// The load-bearing check: a registry file whose pid is not one of the claude
// processes ps found is ignored. Such a file is left behind by a crash, a
// SIGKILL or a reboot, and its pid has very likely been reused since -- acting
// on it resurrects a dead session as running and aims csm's tty lookup, origin
// detection and --kill-ghosts at an unrelated process.
func TestReadSessionRegistryRejectsPIDsNotFoundByPS(t *testing.T) {
	writeRegistry(t, map[string]string{
		"100.json": `{"pid":100,"sessionId":"live","cwd":"/tmp/proj-a"}`,
		"200.json": `{"pid":200,"sessionId":"stale","cwd":"/tmp/proj-a"}`,
	})

	reg, ok := readSessionRegistry(map[int]bool{100: true})
	if !ok {
		t.Fatal("registry directory exists; ok should be true")
	}
	if len(reg) != 1 || reg["live"].PID != 100 {
		t.Errorf("registry = %+v; want only live->100", reg)
	}
	if _, ok := reg["stale"]; ok {
		t.Error("a pid ps did not report as a claude process was accepted")
	}
}

// `claude --resume S` in two tabs puts one session id in two live pid files.
// Nothing distinguishes them, so the id is dropped rather than resolved to
// whichever file happened to be read last.
func TestReadSessionRegistryDropsDuplicateSessionIDs(t *testing.T) {
	writeRegistry(t, map[string]string{
		"100.json": `{"pid":100,"sessionId":"twice","cwd":"/tmp/proj-a"}`,
		"200.json": `{"pid":200,"sessionId":"twice","cwd":"/tmp/proj-a"}`,
		"300.json": `{"pid":300,"sessionId":"once","cwd":"/tmp/proj-a"}`,
	})

	reg, _ := readSessionRegistry(map[int]bool{100: true, 200: true, 300: true})
	if _, ok := reg["twice"]; ok {
		t.Errorf("duplicated session id resolved to %+v; want it dropped", reg["twice"])
	}
	if reg["once"].PID != 300 {
		t.Errorf("unambiguous entry = %+v; want once->300", reg["once"])
	}

	// Dropping it must not demote the session: with its pids no longer named
	// by the registry, pairProcess takes the unknown-process branch and the
	// session stays listed as running with no pid attached.
	running, pid, confident := pairProcess("twice", dirA, reg, true, []int{100, 200, 300}, 0, 2)
	if !running || pid != 0 || confident {
		t.Errorf("duplicated session: running=%v pid=%d confident=%v; want true/0/false",
			running, pid, confident)
	}
}

func TestReadSessionRegistryAbsentDirectory(t *testing.T) {
	orig := sessionsDir
	t.Cleanup(func() { sessionsDir = orig })
	sessionsDir = func() (string, error) { return filepath.Join(t.TempDir(), "missing"), nil }
	if _, ok := readSessionRegistry(map[int]bool{100: true}); ok {
		t.Error("no registry directory; ok should be false so callers fall back")
	}
}

func TestClaudePIDSetFlattensRunningDirs(t *testing.T) {
	got := claudePIDSet(map[string][]int{dirA: {100, 200}, dirB: {300}})
	want := map[int]bool{100: true, 200: true, 300: true}
	if len(got) != len(want) {
		t.Fatalf("claudePIDSet = %v; want %v", got, want)
	}
	for pid := range want {
		if !got[pid] {
			t.Errorf("pid %d missing from %v", pid, got)
		}
	}
}

// Rescue is by transcript path, not by the entry's cwd: EnterWorktree moves a
// session's cwd, and what matters here is where the log actually is.
func TestRegistryLogsForDirMatchesTranscriptPath(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "here.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "moved.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	registry := map[string]registryEntry{
		"here":  {PID: 1, SessionID: "here", Cwd: pathA},
		"moved": {PID: 2, SessionID: "moved", Cwd: "/tmp/proj-a/.claude/worktrees/wt"},
		"nolog": {PID: 3, SessionID: "nolog", Cwd: pathA},
	}
	got := registryLogsForDir(registry, projectDir)
	found := map[string]bool{}
	for _, p := range got {
		found[filepath.Base(p)] = true
	}
	if len(got) != 2 || !found["here.jsonl"] || !found["moved.jsonl"] {
		t.Errorf("got %v; want here.jsonl and moved.jsonl", got)
	}
}
