package session

import (
	"os"
	"path/filepath"
	"testing"
)

func scanOMPFile(t *testing.T, path string) []Flag {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	flags, err := scanOMPFlags(path, info.Size())
	if err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return flags
}

// omp writes the whole bash command into tool_execution_start, so the classifier
// runs off that line alone -- no message body is decoded, and the parse path
// that runs every tick is untouched.
func TestOMPClassifiesCommandsFromTheToolStartEntry(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"custom","customType":"tool_execution_start","timestamp":"2026-01-01T10:00:00Z","data":{"toolCallId":"toolu_a","toolName":"bash","args":{"command":"sudo rm -f /etc/hosts"},"intent":"Fixing hosts"}}`+"\n")

	got := scanOMPFile(t, path)
	if len(got) != 1 || got[0].Rule != RulePrivilegeEscalation {
		t.Fatalf("got %v, want one %s", rules(got), RulePrivilegeEscalation)
	}
	if got[0].ToolUseID != "toolu_a" || got[0].At.IsZero() {
		t.Fatalf("flag lost its call identity: %+v", got[0])
	}
}

// Only bash carries a shell command. A read or an edit has a path in args, and
// running the shell rules over it would classify filenames as commands.
func TestOMPIgnoresNonBashToolStarts(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"custom","customType":"tool_execution_start","timestamp":"2026-01-01T10:00:00Z","data":{"toolCallId":"t1","toolName":"read","args":{"path":"sudo"}}}`+"\n"+
			`{"type":"custom","customType":"tool_execution_start","timestamp":"2026-01-01T10:00:01Z","data":{"toolCallId":"t2","toolName":"web_search","intent":"Researching"}}`+"\n")

	if got := scanOMPFile(t, path); len(got) != 0 {
		t.Fatalf("got %v, want no flags", rules(got))
	}
}

// A user's own rule matching a tool call is omp's one recorded verdict, and it
// says whether the call was stopped or merely warned about. "Ran anyway" is the
// case worth reading, so the two must not collapse.
func TestOMPReportsUserRuleViolations(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"message","timestamp":"2026-01-01T10:00:00Z","message":{"role":"toolResult","toolCallId":"toolu_b","content":[{"type":"text","text":"ok\n<system-reminder reason=\"rule_violation\" rule=\"go-range-int\" path=\"builtin-defaults:go-range-int.md\">\nUser-defined rule matched tool-call arguments. Rule configured not to interrupt → tool ran.\n</system-reminder>"}]}}`+"\n")

	got := scanOMPFile(t, path)
	if len(got) != 1 || got[0].Rule != RuleUserRuleViolation {
		t.Fatalf("got %v, want one %s", rules(got), RuleUserRuleViolation)
	}
	if got[0].Detail != "go-range-int matched; the tool ran anyway" {
		t.Fatalf("detail %q, want the rule name and that it ran", got[0].Detail)
	}
	if got[0].ToolUseID != "toolu_b" {
		t.Fatalf("tool call id %q, want toolu_b", got[0].ToolUseID)
	}
}

// A tool result that merely mentions the phrase is not a verdict. Agents read
// and quote csm's own source and omp's docs, which is how a naive substring
// match on "rule_violation" produced three false positives on a real corpus.
func TestOMPIgnoresTheReminderPhraseInOrdinaryOutput(t *testing.T) {
	resetParseCache()
	dir := t.TempDir()
	path, _, _ := writeLog(t, dir, "s.jsonl",
		`{"type":"message","timestamp":"2026-01-01T10:00:00Z","message":{"role":"toolResult","toolCallId":"t","content":[{"type":"text","text":"grep found: reason=\"rule_violation\" appears in flags_omp.go"}]}}`+"\n")

	if got := scanOMPFile(t, path); len(got) != 0 {
		t.Fatalf("got %v, want no flags", rules(got))
	}
}

// The two stores hold different formats, so the reader is picked from the path.
// Running the Claude rules over an omp log finds nothing and reports a clean
// session, which is worse than an error.
func TestSessionFlagsPicksTheReaderFromTheStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetParseCache()

	ompDir := filepath.Join(home, ".omp", "agent", "sessions", "bucket")
	if err := os.MkdirAll(ompDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ompLog := filepath.Join(ompDir, "2026-01-01T00-00-00-000Z_x.jsonl")
	if err := os.WriteFile(ompLog, []byte(
		`{"type":"custom","customType":"tool_execution_start","timestamp":"2026-01-01T10:00:00Z","data":{"toolCallId":"t","toolName":"bash","args":{"command":"git push --force origin main"}}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := SessionFlags(ompLog)
	if err != nil {
		t.Fatalf("SessionFlags: %v", err)
	}
	if len(got) != 1 || got[0].Rule != RuleForcePush {
		t.Fatalf("got %v, want one %s", rules(got), RuleForcePush)
	}
}
