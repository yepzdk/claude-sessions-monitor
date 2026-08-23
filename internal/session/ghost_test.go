package session

import (
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
