package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeLog writes content to a fresh log file and returns its path plus stat info.
func writeLog(t *testing.T, dir, name, content string) (string, time.Time, int64) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	return p, info.ModTime(), info.Size()
}

// resetParseCache clears the package-level parse cache so tests don't interfere.
func resetParseCache() {
	parseCacheMu.Lock()
	parseCache = map[string]cachedParse{}
	parseCacheMu.Unlock()
}

const sampleLog = `{"type":"summary","summary":"Fix the bug"}
{"type":"user","cwd":"/Users/me/Projects/org/proj","gitBranch":"main","timestamp":"2026-06-01T10:00:00Z","message":{"role":"user","content":"do the thing"}}
{"type":"assistant","timestamp":"2026-06-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"On it"}]}}
`

// Test (a): an unchanged file is parsed only once across repeated lookups.
// We prove the second call did not touch disk by overwriting the file's bytes
// (without changing its size or mtime) and confirming the cached data is returned.
func TestCachedParseLogFile_UnchangedFileNotReparsed(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, mod, size := writeLog(t, dir, "s.jsonl", sampleLog)

	first, err := cachedParseLogFile(path, mod, size, 100)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	if first.summary != "Fix the bug" {
		t.Fatalf("summary = %q, want %q", first.summary, "Fix the bug")
	}

	// Corrupt the file contents in place, keeping the same byte length so size
	// is unchanged, and restore the original mtime. A cache HIT must ignore this.
	corrupt := make([]byte, len(sampleLog))
	for i := range corrupt {
		corrupt[i] = 'x'
	}
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}

	second, err := cachedParseLogFile(path, mod, size, 100)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if second.summary != "Fix the bug" {
		t.Errorf("cache miss: got summary %q from corrupted file, expected cached %q", second.summary, "Fix the bug")
	}
}

// Test (b): changing the file (new mtime/size) triggers a re-parse.
func TestCachedParseLogFile_ChangedFileReparsed(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, mod, size := writeLog(t, dir, "s.jsonl", sampleLog)

	if _, err := cachedParseLogFile(path, mod, size, 100); err != nil {
		t.Fatalf("first parse: %v", err)
	}

	// Append a newer summary and re-stat.
	newContent := sampleLog + `{"type":"summary","summary":"Now ship it"}` + "\n"
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure a distinct mtime even on coarse-grained filesystems.
	future := mod.Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)

	got, err := cachedParseLogFile(path, info.ModTime(), info.Size(), 100)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got.summary != "Now ship it" {
		t.Errorf("summary = %q, want re-parsed %q", got.summary, "Now ship it")
	}
}

// Test: the single-pass parseLogFile extracts the same fields the previous
// three-pass approach (readLastEntries + QuickSessionStats + extractSummary) did.
func TestParseLogFile_ExtractsAllFields(t *testing.T) {
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl", sampleLog)

	pl, err := parseLogFile(path, 100)
	if err != nil {
		t.Fatalf("parseLogFile: %v", err)
	}
	if pl.summary != "Fix the bug" {
		t.Errorf("summary = %q", pl.summary)
	}
	if pl.cwd != "/Users/me/Projects/org/proj" {
		t.Errorf("cwd = %q", pl.cwd)
	}
	if pl.gitBranch != "main" {
		t.Errorf("gitBranch = %q", pl.gitBranch)
	}
	if pl.lastMessage != "On it" {
		t.Errorf("lastMessage = %q", pl.lastMessage)
	}
	// Summary lines are not kept as entries; the two message lines are.
	if len(pl.entries) != 2 {
		t.Errorf("entries = %d, want 2", len(pl.entries))
	}
	if pl.lastEntryTime.IsZero() {
		t.Error("lastEntryTime is zero")
	}
}

// Test (c): on a cache HIT (file unchanged), status is still recomputed against
// the current wall clock, so a session flips Working -> Waiting as time passes
// without the file changing. Exercised through applyParsedLog, which parseSession
// calls on every refresh.
func TestApplyParsedLog_StatusRecomputedOverTime(t *testing.T) {
	// A single assistant text message. determineStatus reports Working when it
	// is within 2 minutes old, and Waiting once older.
	entriesAt := func(age time.Duration) parsedLog {
		return parsedLog{
			entries: []LogEntry{
				{Type: "assistant", Timestamp: time.Now().Add(-age), Message: &Message{
					Content: []ContentItem{{Type: "text", Text: "Working on it"}},
				}},
			},
		}
	}

	var recent Session
	applyParsedLog(&recent, entriesAt(30*time.Second), true, 123, time.Time{})
	if recent.Status != StatusWorking {
		t.Errorf("recent: status = %q, want %q", recent.Status, StatusWorking)
	}

	// Same cached parsedLog contents, but the entry is now old: status must flip.
	var stale Session
	applyParsedLog(&stale, entriesAt(3*time.Minute), true, 123, time.Time{})
	if stale.Status != StatusWaiting {
		t.Errorf("stale: status = %q, want %q", stale.Status, StatusWaiting)
	}
}
