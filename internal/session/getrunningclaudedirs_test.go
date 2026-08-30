package session

import (
	"io/fs"
	"sort"
	"testing"
)

// The orphan set, the claude filter and the cwd-to-project mapping are what the
// dashboard shows. A process filtered wrongly vanishes from it, and an orphan
// flag read from the wrong field badges every session a ghost or none of them.
//
// Driven through the listProcesses seam rather than a procfs fixture, because
// none of this logic is platform-specific.
func TestGetRunningClaudeDirsFiltersAndFlagsWhatTheDashboardShows(t *testing.T) {
	// 101: a claude whose parent is gone. 202: a claude with a live parent.
	// 303: not claude at all. All three share a working directory.
	fakeProcesses(t, []procInfo{
		{pid: 101, ppid: 1, comm: "claude"},
		{pid: 202, ppid: 900, comm: "claude"},
		{pid: 303, ppid: 1, comm: "bash"},
	})

	dirs, orphaned, err := getRunningClaudeDirs()
	if err != nil {
		t.Fatalf("getRunningClaudeDirs: %v", err)
	}

	encoded := encodeProjectPath(fakeCwd)
	got := dirs[encoded]
	sort.Ints(got)
	if len(got) != 2 || got[0] != 101 || got[1] != 202 {
		t.Errorf("pids for %s = %v, want [101 202]; bash must not be counted as a session", encoded, got)
	}
	if len(dirs) != 1 {
		t.Errorf("got %d project keys, want 1: %v", len(dirs), dirs)
	}
	if !orphaned[101] {
		t.Error("the claude whose parent is gone is not flagged an orphan, so it can never be badged a ghost")
	}
	if orphaned[202] {
		t.Error("a claude with a live parent is flagged an orphan, which badges a session left open overnight as a ghost")
	}
}

// A platform returning an empty list with a nil error puts back the bug this
// path exists to remove: every session reads Inactive and csm prints "No active
// Claude sessions." while sessions run.
func TestGetRunningClaudeDirsRejectsAnEmptyProcessTable(t *testing.T) {
	fakeProcesses(t, nil)

	dirs, _, err := getRunningClaudeDirs()
	if err == nil {
		t.Fatalf("an empty process table gave dirs=%v and no error", dirs)
	}
}

// A process whose working directory cannot be read is skipped, not reported. On
// a shared machine another user's claude refuses the read, and an error there
// would be wrong for a machine where "no sessions of yours" is the truth.
func TestGetRunningClaudeDirsStaysSilentWhenAProcessRefusesTheRead(t *testing.T) {
	fakeProcesses(t, []procInfo{{pid: 101, ppid: 1, comm: "claude"}})
	swapCwdLookup(t, func(int) (string, error) { return "", fs.ErrPermission })

	dirs, _, err := getRunningClaudeDirs()
	if err != nil {
		t.Fatalf("a process refusing the read was reported as a failure: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("got %v, want no directories", dirs)
	}
}

// fakeCwd is the working directory the fake lookup reports for every process.
const fakeCwd = "/home/u/proj"

// fakeProcesses points the scan at a fixed process list and gives every process
// the same working directory, for the duration of one test.
//
// Discovery reads an argv as well as a comm, so each process is given the
// simplest argv its comm implies. A test that needs a specific command line
// calls swapArgvLookup afterwards.
func fakeProcesses(t *testing.T, procs []procInfo) {
	t.Helper()
	original := listProcesses
	t.Cleanup(func() { listProcesses = original })
	listProcesses = func() ([]procInfo, error) { return procs, nil }
	swapCwdLookup(t, func(int) (string, error) { return fakeCwd, nil })

	byPID := make(map[int][]string, len(procs))
	for _, p := range procs {
		byPID[p.pid] = []string{p.comm}
	}
	swapArgvLookup(t, func(pid int) ([]string, error) {
		argv, ok := byPID[pid]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return argv, nil
	})
}

// swapArgvLookup replaces the per-process argv lookup for one test.
func swapArgvLookup(t *testing.T, fn func(int) ([]string, error)) {
	t.Helper()
	original := processArgvFn
	t.Cleanup(func() { processArgvFn = original })
	processArgvFn = fn
}

// swapCwdLookup replaces the per-process working-directory lookup. It is a
// separate step so a test can keep the process list and fail only the lookup.
func swapCwdLookup(t *testing.T, fn func(int) (string, error)) {
	t.Helper()
	original := getProcessCwdFn
	t.Cleanup(func() { getProcessCwdFn = original })
	getProcessCwdFn = fn
}

// The bug this guards against is a disagreement, not a wrong answer: discovery
// listing a process the pre-SIGTERM recheck then refuses. csm prints "Found 1
// ghost process(es)" and terminates nothing, every run, while the process
// lives. Both sides call classifyProcess over an argv, so the only way a listed
// pid can fail the recheck is by having been recycled -- which is what the
// recheck is for.
func TestDiscoveryAndTheKillGuardIdentifyAProcessTheSameWay(t *testing.T) {
	// A claude installed under a path holding a space, and a wrapper script
	// whose name merely ends in "claude". Both reach the argv test: comm is a
	// deliberately loose prefilter.
	fakeProcesses(t, []procInfo{
		{pid: 101, ppid: 1, comm: "claude"},
		{pid: 202, ppid: 1, comm: "work-claude"},
	})
	argv := map[int][]string{
		101: {"/Volumes/My Disk/bin/claude", "--resume"},
		202: {"/home/u/bin/work-claude"},
	}
	swapArgvLookup(t, func(pid int) ([]string, error) { return argv[pid], nil })

	dirs, _, err := getRunningClaudeDirs()
	if err != nil {
		t.Fatalf("getRunningClaudeDirs: %v", err)
	}

	discovered := make(map[int]bool)
	for _, pids := range dirs {
		for _, pid := range pids {
			discovered[pid] = true
		}
	}

	for _, tc := range []struct {
		pid   int
		want  bool
		claim string
	}{
		{101, true, "a claude under a path holding a space"},
		{202, false, "a wrapper whose name only ends in claude"},
	} {
		if discovered[tc.pid] != tc.want {
			t.Errorf("%s: discovered = %v, want %v", tc.claim, discovered[tc.pid], tc.want)
		}
		// The recheck has to reach the same verdict, or a discovered ghost is
		// listed and never signalled.
		verifyErr := verifyGhostProcess(tc.pid, HarnessClaude)
		if (verifyErr == nil) != tc.want {
			t.Errorf("%s: discovery says %v but the kill guard says %v (%v)",
				tc.claim, tc.want, verifyErr == nil, verifyErr)
		}
	}
}
