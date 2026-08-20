package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeLogAt writes a minimal log file and sets its mtime explicitly, so
// tests can construct a directory of files with controlled ages.
func writeLogAt(t *testing.T, dir, name string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(`{"type":"user","timestamp":"2026-01-01T00:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

func containsPath(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}

// A genuinely active session that has gone quiet for a while (extended
// thinking, a long tool call, the user stepping away) must not be dropped
// just because an unrelated, merely fresher file exists in the same
// directory. There is no reliable pid<->file correlation to fall back on, so
// findActiveLogs must give a quiet-but-plausibly-active file a second chance
// via activeLogFreshnessWindow rather than excluding it outright.
func TestFindActiveLogs_KeepsQuietActiveSessionAlongsideFresherUnrelatedFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	activeQuiet := writeLogAt(t, dir, "session-active-quiet.jsonl", now.Add(-6*time.Minute))
	writeLogAt(t, dir, "session-unrelated-older.jsonl", now.Add(-10*time.Minute))
	writeLogAt(t, dir, "session-unrelated-newer.jsonl", now.Add(-1*time.Minute))

	// Only one process is actually running in this directory -- the one
	// behind activeQuiet, which has been quiet for 6 minutes.
	result, err := findActiveLogs(dir, 1)
	if err != nil {
		t.Fatal(err)
	}

	if !containsPath(result, activeQuiet) {
		t.Errorf("active session's log missing from result: %v", result)
	}
}

// The widened freshness window must not become "return everything in the
// directory" -- sessions that ended hours or days ago still need to be
// excluded so they don't clutter the dashboard as if they were live.
func TestFindActiveLogs_StillExcludesLongDeadSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	running := writeLogAt(t, dir, "session-running.jsonl", now.Add(-10*time.Second))
	hoursOld := writeLogAt(t, dir, "session-hours-old.jsonl", now.Add(-3*time.Hour))
	daysOld := writeLogAt(t, dir, "session-days-old.jsonl", now.Add(-72*time.Hour))

	result, err := findActiveLogs(dir, 1)
	if err != nil {
		t.Fatal(err)
	}

	if !containsPath(result, running) {
		t.Errorf("running session's own log missing from result: %v", result)
	}
	if containsPath(result, hoursOld) {
		t.Errorf("a session quiet for 3 hours must not be swept in: %v", result)
	}
	if containsPath(result, daysOld) {
		t.Errorf("a session quiet for 3 days must not be swept in: %v", result)
	}
}
