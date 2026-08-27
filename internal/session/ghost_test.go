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
		{Project: "work/api", GhostPID: 5200, LastActivity: stale, PIDConfident: false},
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
			LastActivity: time.Now().Add(-3 * time.Hour), PIDConfident: true},
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
			LastActivity: time.Now().Add(-5 * time.Hour), PIDConfident: true},
	}

	if ghosts := ghostsFrom(sessions); len(ghosts) != 0 {
		t.Fatalf("got %d ghosts, want 0", len(ghosts))
	}
}

func TestGhostsFromDeduplicatesPIDs(t *testing.T) {
	stale := time.Now().Add(-2 * time.Hour)
	sessions := []Session{
		{Project: "a", GhostPID: 77, LastActivity: stale, PIDConfident: true},
		{Project: "b", GhostPID: 77, LastActivity: stale, PIDConfident: true},
	}

	if ghosts := ghostsFrom(sessions); len(ghosts) != 1 {
		t.Fatalf("got %d ghosts for one pid, want 1", len(ghosts))
	}
}

// The badge is only reachable if something sets the flag. determineStatus
// returned a hardcoded false for it on every path, so the derivation is worth
// pinning separately from the filter that decides whether the row is shown.
func TestApplyParsedLogDerivesIsGhost(t *testing.T) {
	tests := []struct {
		name      string
		isRunning bool
		lastEntry time.Time
		want      bool
	}{
		{"running with a long-silent log", true, time.Now().Add(-3 * time.Hour), true},
		{"running and recently active", true, time.Now().Add(-time.Minute), false},
		{"just inside the threshold", true, time.Now().Add(-GhostThreshold + time.Minute), false},
		{"stale but no process", false, time.Now().Add(-3 * time.Hour), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Session
			applyParsedLog(&s, parsedLog{lastEntryTime: tt.lastEntry}, tt.isRunning, 0, tt.lastEntry)
			if s.IsGhost != tt.want {
				t.Errorf("IsGhost = %v, want %v", s.IsGhost, tt.want)
			}
		})
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
	processScanDirs = nil
	processScanRegistry, processScanHaveReg = nil, false
	processScanAt = time.Time{}
	processScanMu.Unlock()

	resultMu.Lock()
	result = nil
	resultAt = time.Time{}
	resultMu.Unlock()
}
