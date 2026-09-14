package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scanFile stats path and scans it, which is what every caller does.
func scanFile(t *testing.T, path string) []Flag {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	flags, err := scanClaudeFlags(path, info.Size())
	if err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return flags
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func rules(flags []Flag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = f.Rule
	}
	return out
}

const sandboxLine = `{"type":"assistant","timestamp":"2026-01-01T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"rm -rf /tmp/x","dangerouslyDisableSandbox":true}}]}}` + "\n"

// The scan resumes from a byte offset, so the second pass must add only what was
// appended. Re-reading the file instead would double every flag already found,
// which is invisible on the first tick and obvious an hour into a session.
func TestScanIsIncrementalAndDoesNotDuplicate(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl", sandboxLine)

	if got := rules(scanFile(t, path)); len(got) != 1 {
		t.Fatalf("first scan: got %v, want one flag", got)
	}
	if got := rules(scanFile(t, path)); len(got) != 1 {
		t.Fatalf("rescan of an unchanged file: got %v, want the same one flag", got)
	}

	appendLine(t, path, sandboxLine)
	got := rules(scanFile(t, path))
	if len(got) != 2 {
		t.Fatalf("after appending a second flagged line: got %v, want two flags", got)
	}
}

// A log is appended to while csm reads it, so the tail is routinely a fragment
// of a line. Consuming it would park the offset mid-line and lose the rest of
// that entry -- and every flag in it -- for good.
func TestPartialTrailingLineIsNotConsumed(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	half := sandboxLine[:len(sandboxLine)/2]
	path, _, _ := writeLog(t, dir, "s.jsonl", half)

	if got := scanFile(t, path); len(got) != 0 {
		t.Fatalf("half-written line: got %v, want no flags yet", rules(got))
	}

	appendLine(t, path, sandboxLine[len(sandboxLine)/2:])
	got := scanFile(t, path)
	if len(got) != 1 || got[0].Rule != RuleSandboxBypass {
		t.Fatalf("after the line completed: got %v, want one %s", rules(got), RuleSandboxBypass)
	}
}

// The offset is only meaningful for a file that grew. A shorter file is a
// different file, and resuming into it would decode from the middle of a line.
func TestShrunkFileIsRescannedFromTheStart(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl", sandboxLine+sandboxLine)
	if got := scanFile(t, path); len(got) != 2 {
		t.Fatalf("setup: got %v, want two flags", rules(got))
	}

	if err := os.WriteFile(path, []byte(sandboxLine), 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	got := scanFile(t, path)
	if len(got) != 1 {
		t.Fatalf("after truncation: got %v, want the one flag the new content holds", rules(got))
	}
}

// Only a move to a more permissive mode is an event. Flagging the first mode a
// session records would flag every session that ever left plan mode once, and
// flagging the way back would report tightening as if it were loosening.
func TestPermissionModeFlagsOnlyEscalation(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name:  "first mode is a baseline",
			lines: []string{`{"type":"permission-mode","permissionMode":"auto"}`},
			want:  nil,
		},
		{
			name: "plan to auto escalates",
			lines: []string{
				`{"type":"permission-mode","permissionMode":"plan"}`,
				`{"type":"permission-mode","permissionMode":"auto"}`,
			},
			want: []string{RulePermissionEscalated},
		},
		{
			name: "auto back to plan does not",
			lines: []string{
				`{"type":"permission-mode","permissionMode":"auto"}`,
				`{"type":"permission-mode","permissionMode":"plan"}`,
			},
			want: nil,
		},
		{
			name: "default to acceptEdits stays below the floor",
			lines: []string{
				`{"type":"permission-mode","permissionMode":"default"}`,
				`{"type":"permission-mode","permissionMode":"acceptEdits"}`,
			},
			want: nil,
		},
		{
			name: "repeating a mode is not a change",
			lines: []string{
				`{"type":"permission-mode","permissionMode":"plan"}`,
				`{"type":"permission-mode","permissionMode":"auto"}`,
				`{"type":"permission-mode","permissionMode":"auto"}`,
			},
			want: []string{RulePermissionEscalated},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetParseCache()
			dir := t.TempDir()
			path, _, _ := writeLog(t, dir, "s.jsonl", strings.Join(tc.lines, "\n")+"\n")
			got := rules(scanFile(t, path))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// Escalating to bypassPermissions outranks escalating to auto: one lets the
// agent act without asking, the other lets a classifier decide.
func TestBypassPermissionsOutranksAuto(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"permission-mode","permissionMode":"auto"}`+"\n"+
			`{"type":"permission-mode","permissionMode":"bypassPermissions"}`+"\n")

	got := scanFile(t, path)
	if len(got) != 1 || got[0].Severity != SeverityHigh {
		t.Fatalf("got %v, want one high-severity flag", got)
	}
}

// A permission-mode entry carries no timestamp of its own. Dating it to the zero
// time makes the flag useless for the one question the feature exists to answer.
// The line before it is usually ordinary prose that no rule reads, so the clock
// has to advance on those lines too.
func TestUntimestampedEntryInheritsTheLastTimestampSeen(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"permission-mode","permissionMode":"plan"}`+"\n"+
			`{"type":"assistant","timestamp":"2026-01-01T10:00:00Z","message":{"role":"assistant","content":"thinking"}}`+"\n"+
			`{"type":"permission-mode","permissionMode":"auto"}`+"\n")

	got := scanFile(t, path)
	if len(got) != 1 {
		t.Fatalf("got %v, want one escalation", rules(got))
	}
	if got := got[0].At.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-01-01T10:00:00Z" {
		t.Fatalf("escalation dated %s, want the preceding entry's timestamp", got)
	}
}

// A refusal by the auto-mode classifier and a refusal by the user are different
// events: one is policy working, the other is the person at the keyboard saying
// no. Collapsing them loses the distinction the flag list is read for.
func TestRefusalsAreClassifiedByWhoRefused(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"user","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"toolu_a","is_error":true,`+
			`"content":"Permission for this action was denied by the Claude Code auto mode classifier."}]}}`+"\n"+
			`{"type":"user","timestamp":"2026-01-01T10:01:00Z","message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"toolu_b","is_error":true,`+
			`"content":[{"type":"text","text":"Permission to use Bash with command rm -rf / has been denied."}]}]}}`+"\n")

	got := scanFile(t, path)
	want := []string{RuleClassifierBlocked, RulePermissionDenied}
	if r := rules(got); len(r) != 2 || r[0] != want[0] || r[1] != want[1] {
		t.Fatalf("got %v, want %v", r, want)
	}
	if got[1].ToolUseID != "toolu_b" {
		t.Fatalf("tool_use_id %q, want toolu_b so the flag can be found in the timeline", got[1].ToolUseID)
	}
}

// An errored tool result is the common case -- a failing command, a missing
// file. Only a refusal is a flag; treating every error as one would bury the
// refusals in noise.
func TestOrdinaryToolErrorIsNotAFlag(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"user","timestamp":"2026-01-01T10:00:00Z","message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"toolu_a","is_error":true,`+
			`"content":"go: no such file or directory"}]}}`+"\n")

	if got := scanFile(t, path); len(got) != 0 {
		t.Fatalf("got %v, want no flags", rules(got))
	}
}

// The command is the whole point of the sandbox-bypass flag: the boolean this
// rule replaced could say a session had bypassed the sandbox but never which
// call did it or when.
func TestSandboxBypassCarriesTheCommandAndTheCall(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl", sandboxLine)

	got := scanFile(t, path)
	if len(got) != 1 {
		t.Fatalf("got %v, want one flag", rules(got))
	}
	f := got[0]
	if f.Detail != "rm -rf /tmp/x" {
		t.Fatalf("detail %q, want the command", f.Detail)
	}
	if f.ToolUseID != "toolu_1" || f.Tool != "Bash" || f.At.IsZero() {
		t.Fatalf("flag lost its call identity: %+v", f)
	}
}

// A Bash call that did not ask for the sandbox off is the overwhelming majority
// of Bash calls.
func TestOrdinaryBashCallIsNotAFlag(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"assistant","timestamp":"2026-01-01T10:00:00Z","message":{"role":"assistant","content":[`+
			`{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`+"\n")

	if got := scanFile(t, path); len(got) != 0 {
		t.Fatalf("got %v, want no flags", rules(got))
	}
}

// The row badge is coloured by the worst severity and counts every flag. Getting
// the worst wrong makes a critical session look routine.
func TestSummaryCountsAndRanksSeverities(t *testing.T) {
	got := summarizeFlags([]Flag{
		{Severity: SeverityMedium},
		{Severity: SeverityCritical},
		{Severity: SeverityHigh},
		{Severity: SeverityMedium},
	})
	want := FlagSummary{Medium: 2, High: 1, Critical: 1, Worst: SeverityCritical}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got.Total() != 4 {
		t.Fatalf("total %d, want 4", got.Total())
	}
	if empty := summarizeFlags(nil); empty != (FlagSummary{}) {
		t.Fatalf("no flags summarised to %+v, want the zero value so the row omits it", empty)
	}
}

// Detail is log-derived and unbounded; a heredoc or a minified blob would
// otherwise put kilobytes into every session payload.
func TestExcerptCollapsesAndBounds(t *testing.T) {
	if got := excerpt("git commit -m 'one\ntwo'"); got != "git commit -m 'one two'" {
		t.Fatalf("got %q, want the newline collapsed", got)
	}
	long := strings.Repeat("é", flagDetailRunes*2)
	got := excerpt(long)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("long detail was not marked as cut: %q", got)
	}
	if n := len([]rune(strings.TrimSuffix(got, "..."))); n != flagDetailRunes {
		t.Fatalf("cut to %d runes, want %d", n, flagDetailRunes)
	}
}

// The flag endpoint takes a path from an HTTP query parameter, so it must refuse
// one outside the projects directory exactly as the timeline and metrics
// endpoints do.
func TestSessionFlagsRejectsAPathOutsideTheProjectsDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetParseCache()
	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.WriteFile(outside, []byte(sandboxLine), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := SessionFlags(outside); err == nil {
		t.Fatal("a log outside the projects directory was accepted")
	}
}
