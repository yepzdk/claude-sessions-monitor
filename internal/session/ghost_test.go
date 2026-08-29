package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A directory with several Claude processes pairs pids to logs positionally,
// and the two orderings are unrelated: logs are sorted newest-first while pids
// arrive in ps order. Reporting a ghost from an unconfident pairing means
// --kill-ghosts sends SIGTERM to whichever process happens to sit at that
// index, which can be the actively working one.
func TestGhostsFromSkipsUnconfidentPairings(t *testing.T) {
	stale := time.Now().Add(-3 * time.Hour)
	busy := time.Now().Add(-10 * time.Second)

	sessions := []Session{
		// Both sessions live in one directory, so neither pid pairing is
		// trustworthy. The stale log carries the busy process's pid.
		{Project: "work/api", GhostPID: 4100, LastActivity: busy, PIDConfident: false},
		{Project: "work/api", GhostPID: 5200, LastActivity: stale, PIDConfident: false, IsGhost: true},
	}

	ghosts := ghostsFrom(sessions)

	if len(ghosts) != 0 {
		t.Fatalf("reported %d ghost(s) from positional pairings; want 0. "+
			"PID %d is the working process and would be killed",
			len(ghosts), ghosts[0].PID)
	}
}

func TestGhostsFromReportsConfidentStaleSession(t *testing.T) {
	sessions := []Session{
		{Project: "work/api", GhostPID: 4100,
			LastActivity: time.Now().Add(-3 * time.Hour), PIDConfident: true, IsGhost: true},
	}

	ghosts := ghostsFrom(sessions)

	if len(ghosts) != 1 {
		t.Fatalf("got %d ghosts, want 1", len(ghosts))
	}
	if ghosts[0].PID != 4100 {
		t.Errorf("PID = %d, want 4100", ghosts[0].PID)
	}
}

func TestGhostsFromIgnoresFreshAndUnrunning(t *testing.T) {
	sessions := []Session{
		// Confident but recently active: not a ghost.
		{Project: "a", GhostPID: 1, LastActivity: time.Now(), PIDConfident: true},
		// Stale but no running process at all.
		{Project: "b", GhostPID: 0,
			LastActivity: time.Now().Add(-5 * time.Hour), PIDConfident: true, IsGhost: true},
		// Confident, running and silent for hours, but still parented to its
		// shell: a tab left open overnight, not a ghost. Killing it would take
		// down a session the user is coming back to.
		{Project: "c", GhostPID: 3,
			LastActivity: time.Now().Add(-9 * time.Hour), PIDConfident: true, IsGhost: false},
	}

	if ghosts := ghostsFrom(sessions); len(ghosts) != 0 {
		t.Fatalf("got %d ghosts, want 0", len(ghosts))
	}
}

func TestGhostsFromDeduplicatesPIDs(t *testing.T) {
	stale := time.Now().Add(-2 * time.Hour)
	sessions := []Session{
		{Project: "a", GhostPID: 77, LastActivity: stale, PIDConfident: true, IsGhost: true},
		{Project: "b", GhostPID: 77, LastActivity: stale, PIDConfident: true, IsGhost: true},
	}

	if ghosts := ghostsFrom(sessions); len(ghosts) != 1 {
		t.Fatalf("got %d ghosts for one pid, want 1", len(ghosts))
	}
}

// A ghost is a process whose parent is gone *and* whose log has been silent.
// Silence on its own used to be enough, which badged every session left open
// overnight and, because Claude Code creates the log lazily, every session in
// its first minutes (its pid was paired with the previous, stale log).
func TestApplyParsedLogDerivesIsGhost(t *testing.T) {
	tests := []struct {
		name      string
		isRunning bool
		orphaned  bool
		lastEntry time.Time
		want      bool
	}{
		{"orphaned with a long-silent log", true, true, time.Now().Add(-3 * time.Hour), true},
		{"orphaned and recently active", true, true, time.Now().Add(-time.Minute), false},
		{"orphaned, just inside the threshold", true, true, time.Now().Add(-GhostThreshold + time.Minute), false},
		{"left open overnight, parent alive", true, false, time.Now().Add(-9 * time.Hour), false},
		{"stale but no process", false, true, time.Now().Add(-3 * time.Hour), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Session
			applyParsedLog(&s, parsedLog{lastEntryTime: tt.lastEntry}, tt.isRunning, 0, tt.orphaned, tt.lastEntry)
			if s.IsGhost != tt.want {
				t.Errorf("IsGhost = %v, want %v", s.IsGhost, tt.want)
			}
		})
	}
}

// The orphan signal is the ppid column and the harness is decided from argv, so
// a parse that drops or shifts a column turns every session into a ghost, none
// into one, or every process into an unrecognised one. argv is the whole
// remainder of the row, not its last field: a command line has arguments.
func TestParsePSOutputReadsColumns(t *testing.T) {
	out := []byte(`  101     1 ttys003 /opt/homebrew/bin/claude --resume
  202  4321 ttys004 claude
  303     1 ?? bun /Users/dev/.bun/bin/omp
  404   303 ?? /bin/zsh -l
garbage line
  505     1 ttys009
`)
	rows := parsePSOutput(out)
	want := []psLine{
		{pid: 101, ppid: 1, tty: "ttys003", argv: "/opt/homebrew/bin/claude --resume"},
		{pid: 202, ppid: 4321, tty: "ttys004", argv: "claude"},
		{pid: 303, ppid: 1, tty: "??", argv: "bun /Users/dev/.bun/bin/omp"},
		{pid: 404, ppid: 303, tty: "??", argv: "/bin/zsh -l"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

// An empty process list and a failed process scan used to be the same value.
// Every session was then marked Inactive and filtered out, so csm printed
// "No active Claude sessions." with total confidence while sessions ran.
func TestDiscoverReportsProcessScanFailure(t *testing.T) {
	// Discover reads the projects directory before it scans processes, so the
	// scan is only reached when that directory exists. Without this the test
	// passes or fails on whether the machine running it happens to use Claude.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	original := listProcesses
	t.Cleanup(func() {
		listProcesses = original
		clearScanCaches()
	})
	listProcesses = func() ([]byte, error) {
		return nil, errors.New("ps: operation not permitted")
	}
	// Both caches would otherwise serve a result from before the swap.
	clearScanCaches()

	_, err := Discover()
	if err == nil {
		t.Fatal("Discover succeeded while the process scan failed; " +
			"the dashboard would report no active sessions")
	}
	if !strings.Contains(err.Error(), "operation not permitted") {
		t.Errorf("error does not carry the cause: %v", err)
	}
}

// clearScanCaches drops the process-scan and whole-result caches so a test
// sees a fresh Discover rather than a value cached before it changed anything.
func clearScanCaches() {
	processScanMu.Lock()
	processScanProcs, processScanValid = nil, false
	processScanRegistry, processScanHaveReg = nil, false
	processScanAt = time.Time{}
	processScanMu.Unlock()

	resultMu.Lock()
	result = nil
	resultAt = time.Time{}
	resultMu.Unlock()
}
