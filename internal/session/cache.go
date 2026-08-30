package session

import (
	"sync"
	"time"
)

// This file holds the caching layer that keeps csm's CPU usage low. Without it,
// every 2-second refresh (from the TUI loop, the SSE hub, and each HTTP request)
// re-scanned every project, re-parsed every session's multi-MB JSONL log three
// times, and re-read the whole process table for every one, all regardless
// of whether anything had changed.
//
// Three caches, all package-level so the speedup is transparent to callers:
//
//  1. parseCache      — parsed log contents keyed by (path, modTime, size).
//     Skips the full-file re-parse when a log is unchanged.
//  2. processScanCache: the running-process scan, TTL-cached.
//  3. resultCache      — the whole Discover() result, TTL-cached, so bursts of
//     concurrent callers within one tick collapse to a single scan.
//
// All three are package state, so tests that go through Discover() must reset
// them between cases — see clearScanCaches and resetParseCache in the tests.

var (
	// resultTTL is how long a full Discover() result is reused. Kept well under
	// the 2s poll interval so the UI stays just as fresh.
	resultTTL = time.Second
	// processScanTTL is how long the running-process scan is reused. The set of
	// running Claude processes changes slowly, so a couple of seconds is safe.
	processScanTTL = 2 * time.Second
)

// --- 1. Per-file parse cache -------------------------------------------------

// cachedParse is one file's parse, valid while the file's (modTime, size) is
// unchanged. It is generic because the two harnesses' logs parse into different
// shapes but want exactly one caching policy: a second hand-rolled copy of this
// would be a second place for that policy to drift.
type cachedParse[T any] struct {
	modTime time.Time
	size    int64
	log     T
	// err is the non-fatal parse error, cached alongside the partial result it
	// belongs to. Without it a truncated log reported Degraded only on the tick
	// that parsed it and looked complete on every cached tick after, so the
	// row's "incomplete data" marker flickered instead of staying put.
	err error
}

var (
	parseCacheMu  sync.Mutex
	parseCache    = map[string]cachedParse[parsedLog]{}
	ompParseCache = map[string]cachedParse[ompParsedLog]{}
)

// isFatalParseError reports whether a parseLogFile error means the result has
// nothing usable and should be discarded entirely, vs. a scan error (e.g. a
// line beyond the scanner's buffer limit) encountered after some entries were
// already gathered, which is still worth keeping. Treating any error as fatal
// used to discard everything already parsed, and the caller then defaulted
// the session to Inactive regardless of whether it was actually running --
// see parseSession's zero-value default.
func isFatalParseError(err error, entryCount int) bool {
	return err != nil && entryCount == 0
}

// parsedEntries is what cachedParseFile needs of a parse result: how much of it
// survived. It exists so the shared cache policy does not take an accessor
// closure from every caller that would only ever be `len(pl.entries)`.
type parsedEntries interface {
	entryCount() int
}

func (pl parsedLog) entryCount() int    { return len(pl.entries) }
func (pl ompParsedLog) entryCount() int { return len(pl.entries) }

// cachedParseLogFile returns the parsed log for logFile, reusing a cached parse
// when the file's (modTime, size) is unchanged since it was last parsed.
func cachedParseLogFile(logFile string, modTime time.Time, size int64, keep int) (parsedLog, error) {
	return cachedParseFile(parseCache, logFile, modTime, size,
		func() (parsedLog, error) { return parseLogFile(logFile, keep) })
}

// cachedParseOMPLogFile is cachedParseLogFile for omp's session logs.
//
// keep is not a parameter: 100 entries is what determineOMPStatus needs to see a
// whole turn's tool calls, and letting callers vary it would mean a cache whose
// hits depend on who asked first.
func cachedParseOMPLogFile(logFile string, modTime time.Time, size int64) (ompParsedLog, error) {
	const keep = 100
	return cachedParseFile(ompParseCache, logFile, modTime, size,
		func() (ompParsedLog, error) { return parseOMPLogFile(logFile, keep) })
}

// cachedParseFile is the shared cache policy: serve an unchanged file from the
// map, otherwise parse outside the lock and store.
func cachedParseFile[T parsedEntries](cache map[string]cachedParse[T], logFile string,
	modTime time.Time, size int64, parse func() (T, error)) (T, error) {
	parseCacheMu.Lock()
	if c, ok := cache[logFile]; ok && c.size == size && c.modTime.Equal(modTime) {
		parseCacheMu.Unlock()
		return c.log, c.err
	}
	parseCacheMu.Unlock()

	// Miss: parse outside the lock (file I/O should not block other lookups).
	pl, err := parse()
	if isFatalParseError(err, pl.entryCount()) {
		var zero T
		return zero, err
	}

	parseCacheMu.Lock()
	cache[logFile] = cachedParse[T]{modTime: modTime, size: size, log: pl, err: err}
	parseCacheMu.Unlock()
	return pl, err
}

// pruneParseCache drops cached parses for log files not in liveFiles. Without it
// the cache would grow unbounded over a long-running server's lifetime, as every
// session's log path lingers forever after the session ends or its file is
// deleted. Discover() calls this each sweep with the paths it actually parsed, so
// the cache tracks the current working set rather than everything ever seen.
func pruneParseCache(liveFiles map[string]struct{}) {
	parseCacheMu.Lock()
	defer parseCacheMu.Unlock()
	pruneCache(parseCache, liveFiles)
	pruneCache(ompParseCache, liveFiles)
}

// pruneCache drops one cache's entries for files not in liveFiles. Callers hold
// parseCacheMu.
func pruneCache[T any](cache map[string]cachedParse[T], liveFiles map[string]struct{}) {
	for path := range cache {
		if _, ok := liveFiles[path]; !ok {
			delete(cache, path)
		}
	}
}

// --- 2. Process-scan cache ---------------------------------------------------

var (
	processScanMu       sync.Mutex
	processScanAt       time.Time
	processScanValid    bool
	processScanProcs    []harnessProcess
	processScanRegistry map[string]registryEntry
	processScanHaveReg  bool
)

// cachedRunningHarnessProcs wraps getRunningHarnessProcs with a short TTL so the
// expensive `ps`/`lsof` subprocess spawns don't run on every refresh. It also
// returns Claude Code's pid registry, snapshotted under the same lock at the
// same moment, plus whether a registry exists at all.
//
// The two are taken together because they are two halves of one answer. Read
// separately, a graceful exit removes the pid file while the departed pid is
// still in the cached process set, and for up to the TTL every unregistered log in
// that directory reads as running-with-no-pid instead of inactive -- a visible
// blink on every exit. Reading them together also absorbs a torn read: Claude
// Code rewrites these files in place, so an unlucky read fails to parse and
// drops an entry, and pinning that to one tick keeps it from recurring.
//
// processScanValid, not a nil check on the slice: no agent running is a
// legitimate result, and treating it as a cache miss would spawn a full
// `ps`/`lsof` sweep on every tick for the users who have nothing open.
func cachedRunningHarnessProcs() ([]harnessProcess, map[string]registryEntry, bool, error) {
	processScanMu.Lock()
	defer processScanMu.Unlock()

	if processScanValid && processScanTTL > 0 && time.Since(processScanAt) < processScanTTL {
		return processScanProcs, processScanRegistry, processScanHaveReg, nil
	}

	procs, err := getRunningHarnessProcs()
	if err != nil {
		// Leave any previous result in place but do not extend its lifetime;
		// a caller asking again should retry the scan rather than be handed
		// a stale map as though it were fresh.
		return nil, nil, false, err
	}
	registry, haveRegistry := readSessionRegistry(claudePIDSet(procs))

	processScanProcs = procs
	processScanRegistry, processScanHaveReg = registry, haveRegistry
	processScanValid = true
	processScanAt = time.Now()
	return processScanProcs, processScanRegistry, processScanHaveReg, nil
}

// --- 3. Discover result cache ------------------------------------------------

var (
	resultMu sync.Mutex
	resultAt time.Time
	result   []Session
)

// cachedResult returns the last Discover() result if it is younger than
// resultTTL, along with whether it was a hit.
func cachedResult() ([]Session, bool) {
	resultMu.Lock()
	defer resultMu.Unlock()
	if result != nil && resultTTL > 0 && time.Since(resultAt) < resultTTL {
		return result, true
	}
	return nil, false
}

// storeResult memoizes a fresh Discover() result.
func storeResult(sessions []Session) {
	resultMu.Lock()
	result = sessions
	resultAt = time.Now()
	resultMu.Unlock()
}
