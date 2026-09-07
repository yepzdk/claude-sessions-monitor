package session

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	parseCache = map[string]cachedParse[parsedLog]{}
	ompParseCache = map[string]cachedParse[ompParsedLog]{}
	parseCacheMu.Unlock()
}

// The cache returns the non-fatal error on every hit, not just the parse that
// produced it, so the Degraded marker stays put instead of flickering. Callers
// must therefore read the partial result rather than gate on err == nil --
// subagent.go did, and a log with one oversized line lost its Task and
// LastActivity permanently, since the oversized line never leaves the file.
func TestCachedParseKeepsPartialResultWithItsError(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// One good entry, then a line past the limit passed below.
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}` +
		"\n" + `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"` +
		strings.Repeat("x", 4096) + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	pl, parseErr := parseLogFileWithLimit(path, 100, 1024)
	if parseErr == nil {
		t.Fatal("expected a scan error for the oversized line")
	}
	if len(pl.entries) == 0 {
		t.Fatal("expected the entries before the oversized line to survive")
	}

	// Both the miss and the subsequent hit must carry the partial result.
	for _, pass := range []string{"miss", "hit"} {
		got, err := cachedParseFile(parseCache, path, info.ModTime(), info.Size(),
			func() (parsedLog, error) { return parseLogFileWithLimit(path, 100, 1024) })
		if err == nil {
			t.Errorf("%s: error dropped; the row would lose its [?] marker", pass)
		}
		if len(got.entries) == 0 {
			t.Errorf("%s: partial result dropped; the caller has nothing to show", pass)
		}
	}
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

// A reslice of entries[len(entries)-keep:] keeps the whole backing array
// alive through pl.entries, and pl sits in the parse cache for as long as the
// session is listed -- so a long log pins memory proportional to its own size
// rather than to keep. This is the one line in the parse-cache change a
// reader cannot check by eye; cap is the only way to observe it.
func TestParseLogFile_TrimmedEntriesDoNotPinTheWholeLog(t *testing.T) {
	dir := t.TempDir()
	const keep = 100
	var content strings.Builder
	for i := range 5000 {
		content.WriteString(`{"type":"assistant","timestamp":"2026-06-01T10:00:` +
			fmt.Sprintf("%02d", i%60) + `Z","message":{"role":"assistant","content":[{"type":"text","text":"line"}]}}` + "\n")
	}
	path, _, _ := writeLog(t, dir, "s.jsonl", content.String())

	pl, err := parseLogFile(path, keep)
	if err != nil {
		t.Fatalf("parseLogFile: %v", err)
	}
	if len(pl.entries) != keep {
		t.Fatalf("entries = %d, want %d", len(pl.entries), keep)
	}
	if cap(pl.entries) > 2*keep {
		t.Errorf("cap %d for %d kept entries: the parse cache pins the whole log", cap(pl.entries), keep)
	}
}

// A line beyond the scanner's max size aborts the scan (bufio.Scanner's
// behavior, not something parseLogFile can avoid), but every entry parsed
// before that point must still come back rather than being thrown away.
func TestParseLogFile_OversizedLineKeepsEarlierEntries(t *testing.T) {
	dir := t.TempDir()
	oversized := `{"type":"assistant","timestamp":"2026-06-01T10:00:10Z","message":{"role":"assistant","content":[{"type":"text","text":"` +
		strings.Repeat("x", 200) + `"}]}}`
	content := sampleLog + oversized + "\n"
	path, _, _ := writeLog(t, dir, "s.jsonl", content)

	// A tiny limit (well under the oversized line's length, well over every
	// other line's) reproduces the scanner hitting bufio.ErrTooLong without
	// allocating a real maxLogLineBytes-sized string in the test.
	pl, err := parseLogFileWithLimit(path, 100, 250)

	if err == nil {
		t.Fatal("parseLogFileWithLimit: want a scan error from the oversized line, got nil")
	}
	// The two entries from sampleLog, parsed before the scanner gave up.
	if len(pl.entries) != 2 {
		t.Errorf("entries = %d, want 2 (the ones parsed before the oversized line)", len(pl.entries))
	}
	if pl.lastMessage != "On it" {
		t.Errorf("lastMessage = %q, want the last entry parsed before the failure", pl.lastMessage)
	}
}

// cachedParseLogFile must not turn a partial scan (some entries recovered,
// then a scan error) into total data loss -- that cascades into parseSession
// defaulting the session to Inactive regardless of whether it's running.
func TestIsFatalParseError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		entryCount int
		want       bool
	}{
		{"no error", nil, 5, false},
		{"no error, no entries (empty file)", nil, 0, false},
		{"error, nothing recovered", bufio.ErrTooLong, 0, true},
		{"error, some entries recovered", bufio.ErrTooLong, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFatalParseError(tt.err, tt.entryCount)
			if got != tt.want {
				t.Errorf("isFatalParseError(%v, %d) = %v, want %v", tt.err, tt.entryCount, got, tt.want)
			}
		})
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
	applyParsedLog(&recent, entriesAt(30*time.Second), true, 123, false, time.Time{})
	if recent.Status != StatusWorking {
		t.Errorf("recent: status = %q, want %q", recent.Status, StatusWorking)
	}

	// Same cached parsedLog contents, but the entry is now old: status must flip.
	var stale Session
	applyParsedLog(&stale, entriesAt(3*time.Minute), true, 123, false, time.Time{})
	if stale.Status != StatusWaiting {
		t.Errorf("stale: status = %q, want %q", stale.Status, StatusWaiting)
	}
}

// The quota and status endpoints rate-limit hard, so the TTL is the only thing
// standing between a dashboard left open all day and a 429. Nothing exercised
// this policy: both callers reach it through a network fetch a test cannot run.
func TestTTLCacheServesOneFetchForTheWholeTTL(t *testing.T) {
	calls := 0
	fetch := func() *int {
		calls++
		v := calls
		return &v
	}

	c := ttlCache[int]{ttl: time.Hour}
	first := c.get(fetch)
	second := c.get(fetch)

	if calls != 1 {
		t.Fatalf("fetch ran %d times inside one TTL, want 1: the endpoint is asked again on every poll", calls)
	}
	if first != second {
		t.Error("a second call returned a different value inside the TTL")
	}
}

// A value older than the TTL must be refetched, or the panel shows a quota that
// stopped moving hours ago.
func TestTTLCacheRefetchesOnceTheValueHasAgedOut(t *testing.T) {
	calls := 0
	fetch := func() *int {
		calls++
		v := calls
		return &v
	}

	c := ttlCache[int]{ttl: time.Minute}
	c.get(fetch)
	c.fetchedAt = time.Now().Add(-2 * time.Minute)
	got := c.get(fetch)

	if calls != 2 {
		t.Fatalf("fetch ran %d times, want 2: a stale value was served past its TTL", calls)
	}
	if got == nil || *got != 2 {
		t.Error("the refetched value was not the one returned")
	}
}

// get caches on a non-nil result, so a fetcher that returns nil is called every
// time. Both real fetchers return a value carrying the failure instead, which is
// what makes a failed call count against the TTL. This pins the contract the
// doc comment states.
func TestTTLCacheDoesNotCacheANilFetch(t *testing.T) {
	calls := 0
	c := ttlCache[int]{ttl: time.Hour}

	for range 2 {
		c.get(func() *int { calls++; return nil })
	}

	if calls != 2 {
		t.Errorf("fetch ran %d times, want 2: a nil result must not be mistaken for a cached value", calls)
	}
}
