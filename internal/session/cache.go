package session

import (
	"sync"
	"time"
)

// This file holds the caching layer that keeps csm's CPU usage low. Without it,
// every 2-second refresh (from the TUI loop, the SSE hub, and each HTTP request)
// re-scanned every project, re-parsed every session's multi-MB JSONL log three
// times, and spawned a `ps`/`lsof` subprocess per Claude process — all regardless
// of whether anything had changed.
//
// Three caches, all package-level so the speedup is transparent to callers:
//
//  1. parseCache      — parsed log contents keyed by (path, modTime, size).
//     Skips the full-file re-parse when a log is unchanged.
//  2. processScanCache — the `ps`/`lsof` running-process scan, TTL-cached.
//  3. resultCache      — the whole Discover() result, TTL-cached, so bursts of
//     concurrent callers within one tick collapse to a single scan.
//
// The TTLs are package vars (not consts) so tests can set them to 0 to disable
// the time-based caches and assert on the parse cache deterministically.

var (
	// resultTTL is how long a full Discover() result is reused. Kept well under
	// the 2s poll interval so the UI stays just as fresh.
	resultTTL = time.Second
	// processScanTTL is how long the running-process scan is reused. The set of
	// running Claude processes changes slowly, so a couple of seconds is safe.
	processScanTTL = 2 * time.Second
)

// --- 1. Per-file parse cache -------------------------------------------------

type cachedParse struct {
	modTime time.Time
	size    int64
	log     parsedLog
}

var (
	parseCacheMu sync.Mutex
	parseCache   = map[string]cachedParse{}
)

// cachedParseLogFile returns the parsed log for logFile, reusing a cached parse
// when the file's (modTime, size) is unchanged since it was last parsed.
func cachedParseLogFile(logFile string, modTime time.Time, size int64, keep int) (parsedLog, error) {
	parseCacheMu.Lock()
	if c, ok := parseCache[logFile]; ok && c.size == size && c.modTime.Equal(modTime) {
		parseCacheMu.Unlock()
		return c.log, nil
	}
	parseCacheMu.Unlock()

	// Miss: parse outside the lock (file I/O should not block other lookups).
	pl, err := parseLogFile(logFile, keep)
	if err != nil {
		return parsedLog{}, err
	}

	parseCacheMu.Lock()
	parseCache[logFile] = cachedParse{modTime: modTime, size: size, log: pl}
	parseCacheMu.Unlock()
	return pl, nil
}

// pruneParseCache drops cached parses for log files not in liveFiles. Without it
// the cache would grow unbounded over a long-running server's lifetime, as every
// session's log path lingers forever after the session ends or its file is
// deleted. Discover() calls this each sweep with the paths it actually parsed, so
// the cache tracks the current working set rather than everything ever seen.
func pruneParseCache(liveFiles map[string]struct{}) {
	parseCacheMu.Lock()
	defer parseCacheMu.Unlock()
	for path := range parseCache {
		if _, ok := liveFiles[path]; !ok {
			delete(parseCache, path)
		}
	}
}

// --- 2. Process-scan cache ---------------------------------------------------

var (
	processScanMu   sync.Mutex
	processScanAt   time.Time
	processScanDirs map[string][]int
)

// cachedRunningClaudeDirs wraps getRunningClaudeDirs with a short TTL so the
// expensive `ps`/`lsof` subprocess spawns don't run on every refresh.
func cachedRunningClaudeDirs() map[string][]int {
	processScanMu.Lock()
	defer processScanMu.Unlock()

	if processScanDirs != nil && processScanTTL > 0 && time.Since(processScanAt) < processScanTTL {
		return processScanDirs
	}

	processScanDirs = getRunningClaudeDirs()
	processScanAt = time.Now()
	return processScanDirs
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
