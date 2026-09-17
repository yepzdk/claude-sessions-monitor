package session

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FlaggedEvent is one flag with enough context to read it outside the session it
// came from: which project, which agent, which log.
//
// Flag is embedded, so the JSON is one flat object rather than a nested one --
// the feed is a list of events, not a list of sessions containing events.
type FlaggedEvent struct {
	Flag
	Project string  `json:"project"`
	Harness Harness `json:"harness"`
	LogFile string  `json:"log_file"`
}

// DiscoverFlags returns every flag recognised in the last N days, newest first,
// across both harnesses.
//
// This is the question the live badge cannot answer: not "is this session doing
// something now" but "what did the agents on this machine do while I was not
// looking". It reads every log in the window, which the per-file cache makes
// cheap after the first sweep -- a log that has not been appended to costs one
// stat.
//
// A store that cannot be read is skipped rather than failing the sweep, the same
// rule Discover applies: the Claude Code half of the feed should not disappear
// because omp's directory is missing.
func DiscoverFlags(days int) ([]FlaggedEvent, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	var events []FlaggedEvent

	if dir, err := ClaudeProjectsDir(); err == nil {
		events = append(events, flagsUnder(dir, HarnessClaude, cutoff, scanClaudeFlags)...)
	}
	if dir, err := ompSessionsDir(); err == nil {
		events = append(events, flagsUnder(dir, HarnessOMP, cutoff, scanOMPFlags)...)
	}

	// Newest first: a feed is read from the top, and the thing worth seeing is
	// the most recent one.
	sort.Slice(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	return events, nil
}

// flagsUnder scans every session log in one store that was written since cutoff.
func flagsUnder(root string, harness Harness, cutoff time.Time,
	scan func(string, int64) ([]Flag, error)) []FlaggedEvent {
	buckets, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var events []FlaggedEvent
	for _, bucket := range buckets {
		if !bucket.IsDir() {
			continue
		}
		// listLogsByRecency already excludes subagent transcripts and carries
		// the stat the cache key needs, so this is one directory read per
		// bucket rather than a glob plus a stat per file.
		logs, err := listLogsByRecency(filepath.Join(root, bucket.Name()))
		if err != nil {
			continue
		}
		for _, log := range logs {
			// Sorted newest first, so the first log older than the window ends
			// the bucket.
			if log.modTime.Before(cutoff) {
				break
			}
			flags, err := scan(log.path, log.size)
			if err != nil && len(flags) == 0 {
				continue
			}
			project := flagFeedProject(log.path, bucket.Name())
			for _, f := range flags {
				if f.At.Before(cutoff) {
					continue
				}
				events = append(events, FlaggedEvent{
					Flag:    f,
					Project: project,
					Harness: harness,
					LogFile: log.path,
				})
			}
		}
	}
	return events
}

// flagFeedProject names the project a log belongs to, preferring the working
// directory the scan already read out of the file over the directory name,
// which is an encoding of that path in Claude Code's case and a lossy
// home-relative one in omp's.
func flagFeedProject(logFile, bucket string) string {
	flagScanMu.Lock()
	cwd := flagScanCache[logFile].cwd
	flagScanMu.Unlock()
	if cwd != "" {
		return extractProjectName(cwd)
	}
	return decodeProjectName(bucket)
}
