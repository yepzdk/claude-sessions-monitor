package session

import (
	"os"
	"path/filepath"
	"testing"
)

// Three sessions in one directory. ps order and log order disagree, which is
// the case the positional pairing gets wrong. With a registry every session
// gets its own pid, confidently, regardless of either order.
func TestPairProcessUsesRegistryWhenPresent(t *testing.T) {
	registry := map[string]registryEntry{
		"aaa": {PID: 300, SessionID: "aaa", Cwd: "/w"},
		"bbb": {PID: 100, SessionID: "bbb", Cwd: "/w"},
		"ccc": {PID: 200, SessionID: "ccc", Cwd: "/w"},
	}
	pids := []int{100, 200, 300}          // ps order
	logs := []string{"aaa", "bbb", "ccc"} // newest-first

	for i, id := range logs {
		running, pid, confident := pairProcess(id, registry, true, pids, i, len(logs))
		if !running || pid != registry[id].PID || !confident {
			t.Errorf("%s: running=%v pid=%d confident=%v; want true/%d/true",
				id, running, pid, confident, registry[id].PID)
		}
	}
}

// A session absent from the registry, in a directory where the registry
// accounts for every process, has no process: not running, no pid. Before
// this it inherited a neighbour's pid and was reported as running.
func TestPairProcessExitedSessionIsNotRunning(t *testing.T) {
	registry := map[string]registryEntry{
		"live": {PID: 100, SessionID: "live", Cwd: "/w"},
	}
	running, pid, confident := pairProcess("gone", registry, true, []int{100}, 0, 2)
	if running || pid != 0 || confident {
		t.Errorf("exited session: running=%v pid=%d confident=%v; want false/0/false",
			running, pid, confident)
	}
}

// A process the registry cannot name (older build, failed registration) keeps
// the old bias: still treated as running, but no pid is guessed for it.
func TestPairProcessUnknownProcessStaysRunningWithoutPID(t *testing.T) {
	registry := map[string]registryEntry{
		"live": {PID: 100, SessionID: "live", Cwd: "/w"},
	}
	running, pid, confident := pairProcess("other", registry, true, []int{100, 555}, 1, 2)
	if !running || pid != 0 || confident {
		t.Errorf("unknown process: running=%v pid=%d confident=%v; want true/0/false",
			running, pid, confident)
	}
}

// No registry directory: behaviour is exactly the previous positional pairing.
func TestPairProcessFallsBackToPositional(t *testing.T) {
	pids := []int{100, 200}
	r, p, c := pairProcess("x", nil, false, pids, 1, 2)
	if !r || p != 200 || c {
		t.Errorf("positional i=1: running=%v pid=%d confident=%v; want true/200/false", r, p, c)
	}
	r, p, c = pairProcess("x", nil, false, []int{100}, 0, 1)
	if !r || p != 100 || !c {
		t.Errorf("single/single: running=%v pid=%d confident=%v; want true/100/true", r, p, c)
	}
	r, p, c = pairProcess("x", nil, false, nil, 0, 1)
	if r || p != 0 || c {
		t.Errorf("no pids: running=%v pid=%d confident=%v; want false/0/false", r, p, c)
	}
}

func TestReadSessionRegistryFiltersDeadAndMalformed(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("100.json", `{"pid":100,"sessionId":"alive","cwd":"/w"}`)
	write("200.json", `{"pid":200,"sessionId":"dead","cwd":"/w"}`)
	write("300.json", `{"pid":300}`)
	write("100.deadbeef.key", `not json`)

	origDir, origAlive := sessionsDir, processAlive
	t.Cleanup(func() { sessionsDir, processAlive = origDir, origAlive })
	sessionsDir = func() (string, error) { return dir, nil }
	processAlive = func(pid int) bool { return pid == 100 }

	reg, ok := readSessionRegistry()
	if !ok {
		t.Fatal("registry directory exists; ok should be true")
	}
	if len(reg) != 1 || reg["alive"].PID != 100 {
		t.Errorf("registry = %+v; want only alive->100", reg)
	}
}

func TestReadSessionRegistryAbsentDirectory(t *testing.T) {
	orig := sessionsDir
	t.Cleanup(func() { sessionsDir = orig })
	sessionsDir = func() (string, error) { return filepath.Join(t.TempDir(), "missing"), nil }
	if _, ok := readSessionRegistry(); ok {
		t.Error("no registry directory; ok should be false so callers fall back")
	}
}

func TestRegistryLogsForDirMatchesEncodedCwd(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "here.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	registry := map[string]registryEntry{
		"here":      {PID: 1, SessionID: "here", Cwd: "/Users/x/proj"},
		"elsewhere": {PID: 2, SessionID: "elsewhere", Cwd: "/Users/x/other"},
		"nolog":     {PID: 3, SessionID: "nolog", Cwd: "/Users/x/proj"},
	}
	got := registryLogsForDir(registry, encodeProjectPath("/Users/x/proj"), projectDir)
	if len(got) != 1 || filepath.Base(got[0]) != "here.jsonl" {
		t.Errorf("got %v; want just here.jsonl", got)
	}
}
