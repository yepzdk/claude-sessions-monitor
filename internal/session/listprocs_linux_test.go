//go:build linux

package session

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The parent pid is the orphan signal. procfs does not escape the command name,
// so a name holding a space or a ")" shifts every field that follows it, and
// the parent pid then reads as some other number. Every session is then badged
// a ghost, or none of them is.
func TestParseProcStatKeepsThePPIDAlignedAfterAnOddCommandName(t *testing.T) {
	tests := []struct {
		name     string
		stat     string
		wantComm string
		wantPPID int
		wantErr  bool
	}{
		{
			name:     "plain name",
			stat:     "1868 (claude) S 487 1868 487 34816 2784 4194304 12345\n",
			wantComm: "claude",
			wantPPID: 487,
		},
		{
			name:     "name holding a close paren",
			stat:     "43 (weird) name) S 9 43 9 0 -1 4194560 99\n",
			wantComm: "weird) name",
			wantPPID: 9,
		},
		{
			name:    "no command field",
			stat:    "44 claude S 9 44\n",
			wantErr: true,
		},
		{
			name:    "cut off before the parent pid",
			stat:    "45 (claude) S\n",
			wantErr: true,
		},
		{
			name:    "parent pid is not a number",
			stat:    "46 (claude) S nope 46\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comm, ppid, err := parseProcStat([]byte(tt.stat))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsed %q as comm=%q ppid=%d; a malformed row must be reported", tt.stat, comm, ppid)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProcStat(%q): %v", tt.stat, err)
			}
			if comm != tt.wantComm || ppid != tt.wantPPID {
				t.Errorf("got comm=%q ppid=%d, want comm=%q ppid=%d", comm, ppid, tt.wantComm, tt.wantPPID)
			}
		})
	}
}

// A process that exits between the directory listing and the read of its stat
// file must not take the scan down with it. If it did, a busy machine would
// intermittently report every Claude session as inactive.
func TestListProcessesNativeSkipsAProcessThatExitedMidScan(t *testing.T) {
	root := t.TempDir()
	writeProcEntry(t, root, "101", "101 (claude) S 1 101\n")
	// A pid directory with no stat file is what a process that has just exited
	// leaves behind for the moment between the listing and the read.
	if err := os.Mkdir(filepath.Join(root, "202"), 0o755); err != nil {
		t.Fatal(err)
	}
	// procfs also carries named entries, which are not processes. Real procfs
	// gives them a stat file too, so without one this fixture would be dropped
	// by the missing-stat check and prove nothing about the pid guard.
	selfDir := filepath.Join(root, "self")
	if err := os.Mkdir(selfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selfDir, "stat"), []byte("101 (claude) S 1 101\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProcEntry(t, root, "303", "303 (bash) S 101 303\n")

	procRootFor(t, root)
	procs, err := listProcessesNative()
	if err != nil {
		t.Fatalf("scan failed because one process was gone: %v", err)
	}

	// Keyed by pid rather than compared in order: the scan promises no order,
	// and asserting one would only pin os.ReadDir's sort.
	byPID := make(map[int]procInfo, len(procs))
	for _, p := range procs {
		byPID[p.pid] = p
	}
	for _, want := range []procInfo{
		{pid: 101, ppid: 1, comm: "claude"},
		{pid: 303, ppid: 101, comm: "bash"},
	} {
		got, ok := byPID[want.pid]
		if !ok {
			t.Errorf("pid %d is missing from the scan", want.pid)
			continue
		}
		if got != want {
			t.Errorf("pid %d = %+v, want %+v", want.pid, got, want)
		}
	}
	if _, ok := byPID[202]; ok {
		t.Error("the process that exited mid-scan is in the result")
	}
	if len(procs) != 2 {
		t.Errorf("got %d processes, want 2: %+v", len(procs), procs)
	}
}

// A procfs that cannot be listed is a broken scan, not an empty machine. A
// table that lists but yields nothing is rejected one level up, in
// getRunningHarnessProcs.
func TestListProcessesNativeReportsABrokenProcfs(t *testing.T) {
	procRootFor(t, filepath.Join(t.TempDir(), "no-such-procfs"))

	procs, err := listProcessesNative()
	if err == nil {
		t.Fatalf("scan returned %d processes and no error", len(procs))
	}
}

// The scan must read the fields procfs actually writes, which a hand-built
// fixture cannot prove. This checks it against the kernel's own output for the
// one process whose pid, parent and name the test already knows.
func TestListProcessesNativeFindsTheRunningTestProcess(t *testing.T) {
	procRootFor(t, "/proc")

	procs, err := listProcessesNative()
	if err != nil {
		t.Fatalf("scanning /proc: %v", err)
	}

	// The kernel publishes the same name in a file of its own, so this compares
	// what the parser pulled out of stat against what procfs says it is.
	wantComm, err := os.ReadFile("/proc/self/comm")
	if err != nil {
		t.Fatalf("reading /proc/self/comm: %v", err)
	}

	self := os.Getpid()
	for _, p := range procs {
		if p.pid != self {
			continue
		}
		if p.ppid != os.Getppid() {
			t.Errorf("ppid = %d, want %d", p.ppid, os.Getppid())
		}
		if got, want := p.comm, strings.TrimSpace(string(wantComm)); got != want {
			t.Errorf("comm = %q, want %q", got, want)
		}
		return
	}
	t.Errorf("the scan did not find this test process (pid %d) among %d processes", self, len(procs))
}

// procRootFor points the scan at a fixture tree for the duration of one test.
func procRootFor(t *testing.T, dir string) {
	t.Helper()
	original := procRoot
	t.Cleanup(func() { procRoot = original })
	procRoot = dir
}

// writeProcEntry creates <root>/<pid>/stat holding one procfs stat line.
func writeProcEntry(t *testing.T, root, pid, stat string) {
	t.Helper()
	if _, err := strconv.Atoi(pid); err != nil {
		t.Fatalf("fixture pid %q is not a number", pid)
	}
	dir := filepath.Join(root, pid)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
}

// procfs stores argv NUL-separated, exactly as the kernel received it. Reading
// it that way is what keeps an agent installed under a path holding a space in
// one piece; re-splitting a printed command line on whitespace is what used to
// break it.
func TestProcessArgvKeepsArgumentsWholeAcrossSpacesAndEmptyValues(t *testing.T) {
	root := t.TempDir()
	writeProcCmdline(t, root, "101", "/Volumes/My Disk/bin/claude\x00--resume\x00")
	// A process can pass an empty argument, and the kernel writes it as two
	// adjacent NULs. Trimming has to leave that one alone.
	writeProcCmdline(t, root, "202", "prog\x00\x00tail\x00")
	// A kernel thread has no command line at all, and neither does a process
	// that has exited but not been reaped.
	writeProcCmdline(t, root, "303", "")
	procRootFor(t, root)

	tests := []struct {
		name string
		pid  int
		want []string
	}{
		{"a path holding a space stays one argument", 101, []string{"/Volumes/My Disk/bin/claude", "--resume"}},
		{"an empty argument between two others survives", 202, []string{"prog", "", "tail"}},
		{"a process with no command line", 303, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := processArgv(tt.pid)
			if err != nil {
				t.Fatalf("processArgv(%d): %v", tt.pid, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("processArgv(%d) = %q, want %q", tt.pid, got, tt.want)
			}
		})
	}
}

// A pid that is gone has to be reported as an error, not as an empty argv. The
// kill guard tells the two apart, and only one of them is worth reporting to
// the user.
func TestProcessArgvReportsAPIDThatIsGone(t *testing.T) {
	procRootFor(t, t.TempDir())

	if argv, err := processArgv(999); err == nil {
		t.Errorf("processArgv on a missing pid returned %q and no error", argv)
	}
}

// writeProcCmdline creates <root>/<pid>/cmdline holding one NUL-separated argv.
func writeProcCmdline(t *testing.T, root, pid, cmdline string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
}
