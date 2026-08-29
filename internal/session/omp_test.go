package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ts formats a timestamp the way omp writes them.
func ts(d time.Duration) string {
	return time.Now().Add(d).UTC().Format(time.RFC3339Nano)
}

// ompLog writes a session log with the physical shape omp produces: the
// fixed-width title slot first, then the header, then entries.
func ompLog(t *testing.T, dir, name string, entries ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"title","v":1,"title":"Review PR 83","source":"auto","pad":"` +
			strings.Repeat(" ", 40) + `"}`,
		`{"type":"session","version":3,"id":"01a04c60-bf01-7000-b006-be8d2755487a",` +
			`"timestamp":"` + ts(-time.Hour) + `","cwd":"/work/api","title":"Review PR 83"}`,
	}
	lines = append(lines, entries...)

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assistant(at string, stopReason, text string) string {
	return fmt.Sprintf(`{"type":"message","id":"a1","parentId":null,"timestamp":%q,`+
		`"message":{"role":"assistant","model":"claude-opus-5","stopReason":%q,`+
		`"content":[{"type":"thinking","text":"hmm"},{"type":"text","text":%q}]}}`,
		at, stopReason, text)
}

func toolStart(at, callID, tool, intent string) string {
	return fmt.Sprintf(`{"type":"custom","id":"c1","timestamp":%q,`+
		`"customType":"tool_execution_start","data":{"toolCallId":%q,"toolName":%q,`+
		`"startedAt":%q,"args":{"path":"x"},"intent":%q}}`, at, callID, tool, at, intent)
}

func toolResult(at, callID, tool string) string {
	return fmt.Sprintf(`{"type":"message","id":"r1","timestamp":%q,`+
		`"message":{"role":"toolResult","toolCallId":%q,"toolName":%q,"isError":false,`+
		`"content":[{"type":"text","text":"ok"}]}}`, at, callID, tool)
}

func userMsg(at, text string) string {
	return fmt.Sprintf(`{"type":"message","id":"u1","timestamp":%q,`+
		`"message":{"role":"user","content":[{"type":"text","text":%q}]}}`, at, text)
}

// The header is not the first physical line: omp reserves a fixed-width slot for
// the current title so a rename does not rewrite the file. A parser that assumed
// line 1 was the header would read no cwd, and every omp session would lose its
// project name and its join to a running process.
func TestParseOMPLogFileReadsTitleSlotAndHeader(t *testing.T) {
	path := ompLog(t, t.TempDir(), "2026-08-29T07-16-43-905Z_01a04c60.jsonl",
		userMsg(ts(-10*time.Minute), "Lets review pr://83"),
		assistant(ts(-5*time.Minute), "stop", "Reviewed it.\nSecond line"),
	)

	pl, err := parseOMPLogFile(path, 100)
	if err != nil {
		t.Fatalf("parseOMPLogFile: %v", err)
	}

	if pl.cwd != "/work/api" {
		t.Errorf("cwd = %q, want /work/api", pl.cwd)
	}
	if pl.sessionID != "01a04c60-bf01-7000-b006-be8d2755487a" {
		t.Errorf("sessionID = %q", pl.sessionID)
	}
	if pl.title != "Review PR 83" {
		t.Errorf("title = %q, want the title slot's value", pl.title)
	}
	if pl.model != "claude-opus-5" {
		t.Errorf("model = %q", pl.model)
	}
	// Thinking blocks are not messages to the user.
	if !strings.HasPrefix(pl.lastMessage, "Reviewed it.") || strings.Contains(pl.lastMessage, "hmm") {
		t.Errorf("lastMessage = %q; want the text block only", pl.lastMessage)
	}
	if len(pl.entries) != 2 {
		t.Errorf("kept %d entries, want 2 (header and title slot are not entries)", len(pl.entries))
	}
	if pl.lastEntryTime.IsZero() {
		t.Error("lastEntryTime is zero; LastActivity would fall back to the file mtime")
	}
}

// One line csm cannot read is one entry lost, not a broken session. omp's own
// loader is lenient for the same reason, and a session that vanished from the
// dashboard over a single malformed line would be the worse failure.
func TestParseOMPLogFileSkipsUnreadableLines(t *testing.T) {
	path := ompLog(t, t.TempDir(), "2026-08-29T07-16-43-905Z_01a04c60.jsonl",
		`{"type":"custom_message","id":"x1","timestamp":"`+ts(-9*time.Minute)+
			`","customType":"ext","content":"a bare string, not blocks"}`,
		`{"type":"custom","id":"x2","customType":"weird","data":["not","an","object"]}`,
		`not json at all`,
		assistant(ts(-time.Minute), "stop", "Still here."),
	)

	pl, err := parseOMPLogFile(path, 100)
	if err != nil {
		t.Fatalf("parseOMPLogFile: %v", err)
	}
	if pl.lastMessage != "Still here." {
		t.Errorf("lastMessage = %q; entries after the bad lines were lost", pl.lastMessage)
	}
}

// A turn can have several tool calls in flight. Claude Code only lets csm count
// tool_use blocks against tool_result blocks, which cannot say *which* call is
// outstanding; omp puts the same toolCallId on both sides, so the answer is
// exact and the reported tool is the one the session is really blocked on.
func TestOMPPendingToolMatchesByToolCallID(t *testing.T) {
	entries := mustEntries(t,
		toolStart(ts(-3*time.Minute), "call-a", "read", "Reading the PR"),
		toolStart(ts(-2*time.Minute), "call-b", "bash", "Checking repo state"),
		toolResult(ts(-2*time.Minute), "call-a", "read"),
	)

	pending, ok := ompPendingTool(entries)
	if !ok {
		t.Fatal("no pending tool found; call-b has no result")
	}
	if pending.ToolCallID != "call-b" {
		t.Errorf("pending = %q, want call-b (call-a was answered)", pending.ToolCallID)
	}

	answered := mustEntries(t,
		toolStart(ts(-3*time.Minute), "call-a", "read", "Reading the PR"),
		toolResult(ts(-2*time.Minute), "call-a", "read"),
	)
	if _, ok := ompPendingTool(answered); ok {
		t.Error("reported a pending tool when every call had a result")
	}
}

func TestDetermineOMPStatusPendingToolAgesIntoNeedsInput(t *testing.T) {
	now := time.Now()

	fresh := mustEntries(t, toolStart(ts(-30*time.Second), "call-a", "bash", "Checking repo state"))
	status, task := determineOMPStatus(fresh, true, now)
	if status != StatusWorking {
		t.Errorf("status = %q, want %q for a tool call started 30s ago", status, StatusWorking)
	}
	if task != "Checking repo state" {
		t.Errorf("task = %q, want the tool call's intent", task)
	}

	stale := mustEntries(t, toolStart(ts(-30*time.Minute), "call-a", "bash", "Checking repo state"))
	status, _ = determineOMPStatus(stale, true, now)
	if status != StatusNeedsInput {
		t.Errorf("status = %q, want %q for a tool call unanswered for 30m", status, StatusNeedsInput)
	}
}

// The write that finishes a turn updates the file's mtime, so a status rule that
// checked mtime before the turn marker would report every completed turn as
// Working and no session would ever look like it was waiting for the user.
func TestDetermineOMPStatusFinishedTurnBeatsFreshMtime(t *testing.T) {
	entries := mustEntries(t, assistant(ts(-time.Second), "stop", "Done."))

	status, _ := determineOMPStatus(entries, true, time.Now())
	if status != StatusWaiting {
		t.Errorf("status = %q, want %q: stopReason \"stop\" ends the turn", status, StatusWaiting)
	}
}

// An interrupted turn is finished too, from the dashboard's point of view: the
// session is back with the user.
func TestDetermineOMPStatusAbortedTurnIsWaiting(t *testing.T) {
	entries := mustEntries(t, assistant(ts(-time.Second), "aborted", "Interrupted."))

	if status, _ := determineOMPStatus(entries, true, time.Now()); status != StatusWaiting {
		t.Errorf("status = %q, want %q for an aborted turn", status, StatusWaiting)
	}
}

// omp records its own shutdown. A process still around past that point is
// between sessions, not working, and its last write must not say otherwise.
func TestDetermineOMPStatusSessionExitIsWaiting(t *testing.T) {
	entries := mustEntries(t,
		assistant(ts(-2*time.Second), "toolUse", "Working on it."),
		`{"type":"custom","id":"e1","timestamp":"`+ts(-time.Second)+
			`","customType":"session_exit","data":{"reason":"dispose","kind":"normal"}}`,
	)

	if status, _ := determineOMPStatus(entries, true, time.Now()); status != StatusWaiting {
		t.Errorf("status = %q, want %q after session_exit", status, StatusWaiting)
	}
}

func TestDetermineOMPStatusNotRunningIsInactive(t *testing.T) {
	entries := mustEntries(t, assistant(ts(-time.Second), "stop", "Done."))

	if status, _ := determineOMPStatus(entries, false, time.Now()); status != StatusInactive {
		t.Errorf("status = %q, want %q with no process", status, StatusInactive)
	}
}

// A prompt with no answer yet is work in progress. omp gives tool results their
// own role, so unlike Claude Code there is no echoed-tool-result case to exclude.
func TestDetermineOMPStatusRecentPromptIsWorking(t *testing.T) {
	entries := mustEntries(t, userMsg(ts(-10*time.Second), "Lets get coding"))

	status, _ := determineOMPStatus(entries, true, time.Time{})
	if status != StatusWorking {
		t.Errorf("status = %q, want %q for a prompt from 10s ago", status, StatusWorking)
	}
}

// omp names logs "<timestamp>_<uuid>.jsonl". Using the whole stem as the session
// id would break --resume matching and give every session a different origin
// cache key than the one omp knows it by.
func TestOMPSessionIDFromLogFile(t *testing.T) {
	got := ompSessionIDFromLogFile(
		"/x/2026-08-29T07-16-43-905Z_01a04c60-bf01-7000-b006-be8d2755487a.jsonl")
	if want := "01a04c60-bf01-7000-b006-be8d2755487a"; got != want {
		t.Errorf("session id = %q, want %q", got, want)
	}
	if got := ompSessionIDFromLogFile("/x/plain.jsonl"); got != "plain" {
		t.Errorf("session id = %q, want the stem when there is no timestamp prefix", got)
	}
}

// omp relocates its store for --profile and --session-dir, so the default path
// is not a guarantee and csm would otherwise report nothing while sessions ran.
func TestOMPSessionsDirHonoursEnvOverride(t *testing.T) {
	t.Setenv(ompSessionsDirEnv, "/tmp/elsewhere")
	dir, err := OMPSessionsDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/elsewhere" {
		t.Errorf("OMPSessionsDir = %q, want the override", dir)
	}
}

// The breadcrumb is the only pid <-> session evidence omp offers, and acting on
// a guess is what PIDConfident exists to prevent: --kill-ghosts and jump both
// refuse an unconfident pairing.
func TestPairOMPProcessConfidentOnlyOnTerminalMatch(t *testing.T) {
	const logFile = "/omp/sessions/-work-api/2026_abc.jsonl"
	procs := map[int]harnessProcess{
		41: {pid: 41, harness: HarnessOMP, tty: "ttys003"},
		42: {pid: 42, harness: HarnessOMP, tty: "ttys009"},
	}

	running, pid, confident := pairOMPProcess(logFile, []int{41, 42},
		map[string]string{logFile: "ttys009"}, procs)
	if !running || pid != 42 || !confident {
		t.Errorf("got (%v, %d, %v), want (true, 42, true) for a matching terminal",
			running, pid, confident)
	}

	// A session under tmux or zellij gets an env-derived terminal id that ps
	// cannot see, and a headless process has no terminal at all.
	running, pid, confident = pairOMPProcess(logFile, []int{41, 42},
		map[string]string{logFile: "%pane-7"}, procs)
	if !running || pid != 0 || confident {
		t.Errorf("got (%v, %d, %v), want (true, 0, false) when no process owns that terminal",
			running, pid, confident)
	}

	running, pid, confident = pairOMPProcess(logFile, nil, nil, procs)
	if running || pid != 0 || confident {
		t.Errorf("got (%v, %d, %v), want (false, 0, false) with no process in the directory",
			running, pid, confident)
	}
}

// Two terminals pointing at one session (a breadcrumb left behind, or the same
// session opened twice) makes the pairing a coin flip over which process a
// pid-consuming action hits. Drop the pairing instead of guessing -- the same
// rule the Claude Code registry path applies to a duplicated session id.
func TestOMPTerminalSessionsDropsAmbiguousLog(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	terminals := filepath.Join(root, "terminal-sessions")
	if err := os.MkdirAll(terminals, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ompSessionsDirEnv, sessions)

	shared := "/omp/sessions/-work-api/shared.jsonl"
	own := "/omp/sessions/-work-api/own.jsonl"
	write := func(name, cwd, logPath string) {
		if err := os.WriteFile(filepath.Join(terminals, name),
			[]byte(cwd+"\n"+logPath+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ttys001", "/work/api", shared)
	write("ttys002", "/work/api", shared)
	write("ttys003", "/work/api", own)

	got := ompTerminalSessions()

	if got[shared] != ompAmbiguousTerminal {
		t.Errorf("shared log resolved to %q; two terminals claim it", got[shared])
	}
	if got[own] != "ttys003" {
		t.Errorf("own log resolved to %q, want ttys003", got[own])
	}
}

// End to end over a real directory tree: bucket layout, the artifact directory
// omp writes beside each log, the header read that identifies the bucket, and
// the join to a running process.
func TestDiscoverOMPFindsSessionsInBuckets(t *testing.T) {
	resetParseCache()
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	t.Setenv(ompSessionsDirEnv, sessions)

	bucket := filepath.Join(sessions, "-work-api")
	log := ompLog(t, bucket, "2026-08-29T07-16-43-905Z_01a04c60.jsonl",
		toolStart(ts(-20*time.Second), "call-a", "bash", "Checking repo state"),
	)
	// omp keeps per-session artifacts in a directory beside the log. It must not
	// be mistaken for a session.
	if err := os.MkdirAll(strings.TrimSuffix(log, ".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}

	procs := []harnessProcess{
		{pid: 77, harness: HarnessOMP, cwd: "/work/api", tty: "ttys003"},
		// Another agent in the same directory must not be counted here.
		{pid: 78, harness: HarnessClaude, cwd: "/work/api", tty: "ttys004"},
	}

	liveFiles := map[string]struct{}{}
	got := discoverOMP(procs, liveFiles)

	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.Harness != HarnessOMP {
		t.Errorf("Harness = %q, want %q", s.Harness, HarnessOMP)
	}
	if s.Status != StatusWorking {
		t.Errorf("Status = %q, want %q: a process is running and a tool just started",
			s.Status, StatusWorking)
	}
	if s.Task != "Checking repo state" {
		t.Errorf("Task = %q, want the tool call's intent", s.Task)
	}
	if s.CWD != "/work/api" {
		t.Errorf("CWD = %q, want the header's cwd", s.CWD)
	}
	if s.SessionTitle != "Review PR 83" {
		t.Errorf("SessionTitle = %q, want the title slot's value", s.SessionTitle)
	}
	// Blank on purpose: omp is multi-provider, so a context window cannot be
	// derived from the model id, and a wrong percentage reads as a measurement.
	if s.ContextPercent != 0 || s.ContextWindow != 0 {
		t.Errorf("context = %.0f%% of %d; both must stay blank for omp sessions",
			s.ContextPercent, s.ContextWindow)
	}
	if _, ok := liveFiles[log]; !ok {
		t.Error("log missing from liveFiles; its parse would be evicted every sweep")
	}
}

// A missing store is the normal case for anyone running only one of the two
// agents, and must not fail the sweep that also carries the Claude sessions.
func TestDiscoverOMPToleratesMissingStore(t *testing.T) {
	t.Setenv(ompSessionsDirEnv, filepath.Join(t.TempDir(), "absent"))

	if got := discoverOMP(nil, map[string]struct{}{}); got != nil {
		t.Errorf("got %+v, want no sessions and no panic", got)
	}
}

// mustEntries parses raw JSONL lines through the real parser, so a test's
// fixtures are held to the same shape the production path accepts.
func mustEntries(t *testing.T, lines ...string) []ompEntry {
	t.Helper()
	path := ompLog(t, t.TempDir(), "2026-08-29T07-16-43-905Z_01a04c60.jsonl", lines...)
	pl, err := parseOMPLogFile(path, 100)
	if err != nil {
		t.Fatalf("parseOMPLogFile: %v", err)
	}
	if len(pl.entries) != len(lines) {
		t.Fatalf("parsed %d of %d fixture lines; a fixture is malformed",
			len(pl.entries), len(lines))
	}
	return pl.entries
}
