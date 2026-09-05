package session

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// discoverSubagents parses each subagent log through the same cache the main
// logs use, but Discover's prune keeps only the paths it collected while
// walking the project directories, and subagent logs are not among them. The
// prune therefore evicted every subagent parse on the sweep that created it,
// and the next tick re-read and re-decoded logs nothing had written to -- the
// bulk of a sweep's work on a session running agents, repeated every 2 seconds
// for as long as the agents were listed.
func TestDiscoverKeepsSubagentParsesPastThePrune(t *testing.T) {
	resetParseCache()
	clearScanCaches()
	t.Cleanup(func() {
		resetParseCache()
		clearScanCaches()
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, ".claude", "projects", encodeProjectPath(fakeCwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A session whose assistant turn is still waiting on a subagent's result.
	recent := time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339Nano)
	sessionLog := filepath.Join(projectDir, "11111111-2222-3333-4444-555555555555.jsonl")
	writeLog(t, projectDir, filepath.Base(sessionLog),
		`{"type":"user","timestamp":"`+recent+`","cwd":"`+fakeCwd+`"}`+"\n"+
			`{"type":"assistant","timestamp":"`+recent+`","message":{"content":[{"type":"tool_use","id":"call-1","name":"Task"}]}}`+"\n")

	// Its subagent's log, in the sibling directory discoverSubagents reads.
	subDir := subagentsDir(sessionLog)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	subLog := filepath.Join(subDir, "agent-abc.jsonl")
	writeLog(t, subDir, "agent-abc.jsonl",
		`{"type":"assistant","timestamp":"`+recent+`","message":{"content":[{"type":"text","text":"Reading files"}]}}`+"\n")
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc.meta.json"),
		[]byte(`{"agentType":"explore","description":"Find callers","toolUseId":"call-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeProcesses(t, []procInfo{{pid: 4242, ppid: 900, comm: "claude"}})

	sessions, err := Discover()
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	var found *Session
	for i := range sessions {
		if sessions[i].LogFile == sessionLog {
			found = &sessions[i]
		}
	}
	if found == nil {
		t.Fatalf("the session under test was not discovered; got %d sessions", len(sessions))
	}
	if len(found.Subagents) != 1 {
		t.Fatalf("got %d subagents, want 1: the fixture no longer reaches the parse this test is about",
			len(found.Subagents))
	}
	if found.Subagents[0].LogFile != subLog {
		t.Fatalf("subagent LogFile = %q, want %q", found.Subagents[0].LogFile, subLog)
	}

	parseCacheMu.Lock()
	_, cached := parseCache[subLog]
	parseCacheMu.Unlock()
	if !cached {
		t.Error("the subagent's parse was evicted by the sweep that created it; " +
			"every following tick re-reads and re-decodes a log nothing has written to")
	}
}

// Subagent rows swapped places on every refresh. The list was ordered by
// LastActivity, which each agent's log advances at its own moment, while the UI
// prints "Now" for every one of them -- so the rows moved on a difference the
// user could not see. Order must not depend on LastActivity at all.
func TestSubagentOrderHoldsWhenActivityJitters(t *testing.T) {
	resetParseCache()
	t.Cleanup(resetParseCache)

	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, ".claude", "projects", encodeProjectPath(fakeCwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionLog := filepath.Join(projectDir, "11111111-2222-3333-4444-555555555555.jsonl")
	subDir := subagentsDir(sessionLog)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two live subagents, rewritten so that whichever one wrote last flips
	// between the two ticks. "aaa" sorts first by ID on both.
	tick := func(newest string) []string {
		t.Helper()
		resetParseCache()
		for _, id := range []string{"aaa", "bbb"} {
			age := 30 * time.Second
			if id == newest {
				age = 2 * time.Second
			}
			ts := time.Now().Add(-age).UTC().Format(time.RFC3339Nano)
			writeLog(t, subDir, "agent-"+id+".jsonl",
				`{"type":"assistant","timestamp":"`+ts+`","message":{"content":[{"type":"text","text":"working"}]}}`+"\n")
		}
		var ids []string
		for _, sa := range discoverSubagents(sessionLog, map[string]bool{}, true) {
			ids = append(ids, sa.ID)
		}
		return ids
	}

	first, second := tick("bbb"), tick("aaa")

	want := []string{"aaa", "bbb"}
	if !slices.Equal(first, want) || !slices.Equal(second, want) {
		t.Errorf("subagent order = %v then %v, want %v on both ticks; the rows move when only LastActivity changes",
			first, second, want)
	}
}
