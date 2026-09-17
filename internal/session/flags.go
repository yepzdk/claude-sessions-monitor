package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Flags are the notable things csm recognises in a session's log: an agent
// asking for more authority than it had, a tool call the harness refused, a
// command that ran with the sandbox switched off.
//
// Two properties of this feature are load-bearing and easy to erode:
//
//   - It is a report, never a guard. csm reads logs on a timer, so by the time
//     a flag exists the command has already run. Nothing here prevents anything,
//     and no surface may imply that it does.
//   - Absence of flags is not safety. A command assembled at runtime
//     (`bash -c "$(echo … | base64 -d)"`) is invisible to every rule below. No
//     view may render "no flags" as a clean bill of health.
//
// The rules in this file are the harness's *own* verdicts, re-reported. csm
// forms no opinion of its own here: every flag corresponds to a decision Claude
// Code recorded in the log. That is what makes them free of false positives,
// and it is why they are worth having before any classifier of csm's own. The
// rules csm decides for itself live in flags_command.go.

// Severity ranks a flag by how much attention it deserves. It is a string so the
// JSON payload reads without a lookup table; rank supplies the ordering that the
// string cannot.
//
// There is deliberately no "low". A tier was measured and cut: every rule that
// produced one -- `pkill` on a dev server, `rm -rf` in a temp or build
// directory -- fired on ordinary work and never on anything a reader wanted.
// A severity that always means "ignore me" teaches people to ignore the badge.
type Severity string

const (
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// rank orders severities. The zero value (an unset Severity) ranks below every
// real one, so a zero FlagSummary.Worst loses every comparison.
func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityHigh:
		return 2
	case SeverityMedium:
		return 1
	}
	return 0
}

// Rule ids. They are part of the JSON payload and the web page switches on
// them, so they are constants rather than literals at the point of detection.
const (
	// RuleSandboxBypass: a Bash call ran with dangerouslyDisableSandbox set.
	RuleSandboxBypass = "sandbox-bypass"
	// RulePermissionEscalated: the session moved to a more permissive
	// permission mode than it was in.
	RulePermissionEscalated = "permission-escalated"
	// RuleBypassMode: the session is running with permission prompts bypassed
	// entirely.
	RuleBypassMode = "bypass-mode"
	// RulePermissionDenied: a tool call was refused, by the user or by policy.
	RulePermissionDenied = "permission-denied"
	// RuleClassifierBlocked: auto mode's own classifier refused a tool call.
	RuleClassifierBlocked = "classifier-blocked"
)

// flagDetailRunes caps the log-derived excerpt carried on a flag. Long enough to
// recognise a command, short enough that a flag list stays readable and a
// pathological line cannot bloat the API payload.
const flagDetailRunes = 160

// Flag is one notable event csm recognised in a session's log.
//
// Detail and Tool come from the log and are therefore attacker-controllable: a
// command can carry ANSI escapes or HTML. Every render site must sanitize them
// (sanitizeForTerminal in the TUI, esc in the web page).
type Flag struct {
	Rule      string    `json:"rule"`
	Severity  Severity  `json:"severity"`
	At        time.Time `json:"at"`
	Tool      string    `json:"tool,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	ToolUseID string    `json:"tool_use_id,omitempty"` // correlates a flag with its timeline entry
}

// FlagSummary is what a session row carries: enough to badge it, not the list.
//
// The list is per-session detail served separately, the same split
// /api/sessions/metrics already makes for token counts. A session in a long
// unattended run can accumulate dozens of flags, and the row is broadcast to
// every SSE client every two seconds.
type FlagSummary struct {
	Medium   int      `json:"medium,omitempty"`
	High     int      `json:"high,omitempty"`
	Critical int      `json:"critical,omitempty"`
	Worst    Severity `json:"worst,omitempty"`
}

// Total is the number of flags the summary counts.
func (f FlagSummary) Total() int { return f.Medium + f.High + f.Critical }

// summarizeFlags counts flags by severity and records the worst one seen.
func summarizeFlags(flags []Flag) FlagSummary {
	var s FlagSummary
	for _, f := range flags {
		switch f.Severity {
		case SeverityCritical:
			s.Critical++
		case SeverityHigh:
			s.High++
		case SeverityMedium:
			s.Medium++
		}
		if f.Severity.rank() > s.Worst.rank() {
			s.Worst = f.Severity
		}
	}
	return s
}

// --- The incremental scan ----------------------------------------------------

// flagScan is one log file's accumulated flags plus the position it has been
// read to.
//
// Unlike parseCache, which replaces a file's whole parse whenever the file
// changes, this cache *accumulates*: flags are facts about bytes already
// written, and session logs are append-only, so re-reading them would only
// rediscover what is already here. That is also what lets a flag survive past
// the 100-entry window parsedLog keeps -- a sudo at the start of a thousand-turn
// session is exactly the event this feature exists to remember, and the live
// parse cannot see it.
type flagScan struct {
	offset int64 // bytes consumed; only whole, in-limit lines are counted
	flags  []Flag
	mode   string    // last permission mode seen, for the escalation rule
	seen   time.Time // last timestamp seen, for entries that carry none
	// cwd is the working directory the log records, kept so the flag feed can
	// name the project without a second pass over the file.
	cwd string
}

var (
	flagScanMu    sync.Mutex
	flagScanCache = map[string]flagScan{}
)

// SessionFlags returns every flag recognised in a session log, from either
// harness.
//
// It is the read path behind the per-session flag detail; the live sweep gets
// the same slice through scanClaudeFlags or scanOMPFlags and keeps only the
// summary. The path is validated here, as in ParseTimeline and ParseMetrics,
// because it arrives from an HTTP query parameter -- and the store it resolves
// to picks the rule set, since the two formats share no field.
func SessionFlags(logFile string) ([]Flag, error) {
	root, err := validateSessionLogPath(logFile)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(logFile)
	if err != nil {
		return nil, err
	}
	if ompDir, err := ompSessionsDir(); err == nil && sameResolvedDir(root, ompDir) {
		return scanOMPFlags(logFile, info.Size())
	}
	return scanClaudeFlags(logFile, info.Size())
}

// sameResolvedDir reports whether root is dir with symlinks resolved. root
// already came back resolved from validateSessionLogPath.
func sameResolvedDir(root, dir string) bool {
	resolved, err := filepath.EvalSymlinks(dir)
	return err == nil && resolved == root
}

// scanClaudeFlags returns every flag in a Claude Code session log, reading only
// the bytes appended since the last call.
func scanClaudeFlags(logFile string, size int64) ([]Flag, error) {
	return scanFlagsWith(logFile, size, claudeLineFlags)
}

// scanFlagsWith is the shared scan: resume from the cached offset, apply the
// harness's rules to each complete new line, and keep the flags found. One
// policy, two rule sets -- unlike the parse caches, which are two maps because
// the two formats parse into different shapes, a flag is a flag.
//
// The returned slice is the cache's own and must not be mutated by callers; it
// is append-only and grows across calls.
//
// Known ceiling: a line longer than maxLogLineBytes stops the scan where it
// stands, and every later call retries from the same byte and stops again, so
// nothing past that line is ever flagged. The full parse aborts on the same line
// and marks the session Degraded, so the row already says its data is partial.
func scanFlagsWith(logFile string, size int64, rules func([]byte, *flagScan) []Flag) ([]Flag, error) {
	flagScanMu.Lock()
	defer flagScanMu.Unlock()

	sc := flagScanCache[logFile]
	// A file that shrank was not appended to: it was replaced or truncated, and
	// the offset now points into unrelated bytes. Start over rather than emit
	// flags decoded from the middle of a line.
	if size < sc.offset {
		sc = flagScan{}
	}
	if size == sc.offset {
		return sc.flags, nil
	}

	file, err := os.Open(logFile)
	if err != nil {
		return sc.flags, err
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Seek(sc.offset, io.SeekStart); err != nil {
		return sc.flags, err
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLineBytes)
	scanner.Split(scanCompleteLines)

	for scanner.Scan() {
		line := scanner.Bytes()
		sc.offset += int64(len(line)) + 1 // the newline scanCompleteLines consumed
		// Both harnesses write the working directory into the log, Claude Code on
		// every entry and omp on its header. First one wins; the check costs a
		// byte scan only until it is found.
		if sc.cwd == "" {
			sc.cwd = lineStringField(line, cwdKey)
		}
		sc.flags = append(sc.flags, rules(line, &sc)...)
	}

	err = scanner.Err()
	flagScanCache[logFile] = sc
	return sc.flags, err
}

// scanCompleteLines is bufio.ScanLines minus the final unterminated token.
//
// A session log is being appended to while csm reads it, so the last line is
// routinely half-written. bufio.ScanLines hands that fragment over as if it were
// a line; here it must stay unconsumed so the next scan sees it whole and the
// offset never lands mid-line.
func scanCompleteLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, bytes.TrimSuffix(data[:i], []byte("\r")), nil
	}
	if atEOF {
		return 0, nil, nil // stop; leave the fragment for the next scan
	}
	return 0, nil, nil // ask for more data
}

// pruneFlagScanCache drops accumulated flags for log files not in liveFiles,
// alongside pruneParseCache and for the same reason: without it every log path
// csm ever listed stays in memory for the life of the process.
func pruneFlagScanCache(liveFiles map[string]struct{}) {
	flagScanMu.Lock()
	defer flagScanMu.Unlock()
	for path := range flagScanCache {
		if _, ok := liveFiles[path]; !ok {
			delete(flagScanCache, path)
		}
	}
}

// flagNeedles are the substrings a line must contain to be capable of producing
// a flag. Checking them costs a few byte scans and skips a full json.Unmarshal
// on every line that is only prose -- the majority of a session log, and the
// multi-hundred-kilobyte ones at that.
//
// Like extractStringField and lineTimestamp, these assume the writer emits
// compact JSON with no space after a colon, which both harnesses do. A needle
// carries no value for the same reason: `"is_error"` is one fact to be wrong
// about instead of two.
var flagNeedles = [][]byte{
	[]byte(`"permissionMode"`),
	[]byte(`"auto_mode"`),
	[]byte(`"tool_use"`),
	[]byte(`"is_error"`),
}

// flagLine is the decode shape the rules need.
//
// It is deliberately not LogEntry: permissionMode, attachment and a content
// block's is_error are exactly the fields LogEntry drops, and widening LogEntry
// to reach them would change what the status rules, the timeline and the metrics
// pass all see.
type flagLine struct {
	Type           string    `json:"type"`
	Timestamp      time.Time `json:"timestamp"`
	PermissionMode string    `json:"permissionMode"`
	Attachment     *struct {
		Type   string `json:"type"`
		Bypass bool   `json:"bypass"`
	} `json:"attachment"`
	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// flagBlock is one content block, decoded far enough for the rules.
type flagBlock struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	ToolUseID string          `json:"tool_use_id"`
	Input     json.RawMessage `json:"input"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// permissionRank orders Claude Code's permission modes from most to least
// restrictive, so "the agent gained authority" is a comparison rather than a
// list of pairs. An unknown mode ranks 0 and can only ever be escalated *from*,
// which is the safe direction to be wrong in: a new mode name reads as
// restrictive and its first transition to a known permissive mode still flags.
func permissionRank(mode string) int {
	switch mode {
	case "plan":
		return 1
	case "default":
		return 2
	case "acceptEdits":
		return 3
	case "auto":
		return 4
	case "bypassPermissions":
		return 5
	}
	return 0
}

// permissionEscalationFloor is the rank at or above which entering a mode is
// worth a flag. acceptEdits and below are ordinary working modes; auto and
// bypassPermissions let the agent act without asking.
const permissionEscalationFloor = 4

// claudeLineFlags applies every Claude Code rule to one raw log line, using sc
// for the little state the rules carry between lines.
func claudeLineFlags(line []byte, sc *flagScan) []Flag {
	// Claude Code writes permission-mode entries with no timestamp of their own,
	// so an escalation is dated from the last entry that had one: the mode
	// changed at or after it. That means the clock has to advance on lines no
	// rule reads, because the line before a mode change is usually ordinary
	// prose -- which is why this is a byte scan rather than a decode.
	if !hasNeedle(line, flagNeedles) {
		if t, ok := lineTimestamp(line); ok {
			sc.seen = t
		}
		return nil
	}
	var fl flagLine
	if json.Unmarshal(line, &fl) != nil {
		return nil
	}
	if fl.Timestamp.IsZero() {
		fl.Timestamp = sc.seen
	} else {
		sc.seen = fl.Timestamp
	}

	var flags []Flag
	if f, ok := permissionModeFlag(&fl, &sc.mode); ok {
		flags = append(flags, f)
	}
	if f, ok := bypassModeFlag(&fl); ok {
		flags = append(flags, f)
	}
	return append(flags, contentBlockFlags(&fl)...)
}

// hasNeedle reports whether a line contains any of the substrings that make it
// worth decoding.
func hasNeedle(line []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(line, n) {
			return true
		}
	}
	return false
}

// The fields lineStringField is asked for. Both assume compact JSON; see
// flagNeedles.
var (
	timestampKey = []byte(`"timestamp":"`)
	cwdKey       = []byte(`"cwd":"`)
)

// lineStringField pulls one string field out of a raw line without decoding the
// rest of it, the same trick parseLogFileWithLimit uses for cwd and title.
//
// It does not unescape: a value containing \" ends early. Both fields it is used
// for -- a timestamp and a filesystem path -- cannot contain one in practice,
// and a truncated path is a worse project name rather than a wrong flag.
func lineStringField(line, key []byte) string {
	i := bytes.Index(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return string(rest[:j])
}

// lineTimestamp is lineStringField parsed as a time.
func lineTimestamp(line []byte) (time.Time, bool) {
	s := lineStringField(line, timestampKey)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// permissionModeFlag reports a move into a more permissive mode.
//
// The first mode a session records is a baseline, not a change: a session that
// starts in auto never escalated into it. That posture is caught by
// bypassModeFlag instead, which reads the mode the harness announces rather than
// the transitions between them.
func permissionModeFlag(fl *flagLine, mode *string) (Flag, bool) {
	if fl.PermissionMode == "" {
		return Flag{}, false
	}
	prev := *mode
	*mode = fl.PermissionMode
	if prev == "" || fl.PermissionMode == prev {
		return Flag{}, false
	}
	to := permissionRank(fl.PermissionMode)
	if to < permissionEscalationFloor || to <= permissionRank(prev) {
		return Flag{}, false
	}
	sev := SeverityMedium
	if fl.PermissionMode == "bypassPermissions" {
		sev = SeverityHigh
	}
	return Flag{
		Rule:     RulePermissionEscalated,
		Severity: sev,
		At:       fl.Timestamp,
		Detail:   prev + " -> " + fl.PermissionMode,
	}, true
}

// bypassModeFlag reports auto mode running with permission prompts bypassed
// outright, which a transition cannot show: a session launched that way never
// changes mode.
func bypassModeFlag(fl *flagLine) (Flag, bool) {
	if fl.Attachment == nil || fl.Attachment.Type != "auto_mode" || !fl.Attachment.Bypass {
		return Flag{}, false
	}
	return Flag{
		Rule:     RuleBypassMode,
		Severity: SeverityHigh,
		At:       fl.Timestamp,
		Detail:   "auto mode is bypassing permission prompts",
	}, true
}

// contentBlockFlags applies the rules that read a message's content blocks.
func contentBlockFlags(fl *flagLine) []Flag {
	if fl.Message == nil || len(fl.Message.Content) == 0 {
		return nil
	}
	var blocks []flagBlock
	if json.Unmarshal(fl.Message.Content, &blocks) != nil {
		return nil // a bare-string content carries no tool call
	}

	var flags []Flag
	for i := range blocks {
		b := &blocks[i]
		switch {
		case b.Type == "tool_use":
			flags = append(flags, bashFlags(b, fl.Timestamp)...)
		case b.Type == "tool_result" && b.IsError:
			if f, ok := deniedFlag(b, fl.Timestamp); ok {
				flags = append(flags, f)
			}
		}
	}
	return flags
}

// bashFlags applies both the harness's verdict and csm's own classifier to one
// tool_use block.
//
// The sandbox bypass is the harness's own record -- the rule the HasUnsandboxed
// boolean used to be, now carrying when it happened and which command asked.
// Everything after it is csm's own reading of the command; see
// flags_command.go. Only Bash carries a shell command, so every other tool's
// input is left as the raw bytes the timeline already shows.
func bashFlags(b *flagBlock, at time.Time) []Flag {
	if b.Name != "Bash" || len(b.Input) == 0 {
		return nil
	}
	var input BashToolInput
	if json.Unmarshal(b.Input, &input) != nil {
		return nil
	}

	var flags []Flag
	if input.DangerouslyDisableSandbox {
		flags = append(flags, Flag{
			Rule:      RuleSandboxBypass,
			Severity:  SeverityHigh,
			At:        at,
			Tool:      b.Name,
			Detail:    excerpt(input.Command),
			ToolUseID: b.ID,
		})
	}
	return append(flags, commandFlags(input.Command, at, b.ID)...)
}

// deniedFlag reports a refused tool call.
//
// Claude Code records the refusal as prose in an errored tool_result -- there is
// no structured "denied" field to read -- so this matches on the sentence it
// writes. That makes the rule version-sensitive by construction: a reworded
// message stops matching and the flag silently disappears. It is worth having
// anyway, because the alternative is not reporting refusals at all, and a
// missed flag degrades to today's behaviour rather than to a wrong one.
func deniedFlag(b *flagBlock, at time.Time) (Flag, bool) {
	text := blockText(b.Content)
	if !strings.Contains(text, "denied") {
		return Flag{}, false
	}
	if !strings.HasPrefix(text, "Permission ") {
		return Flag{}, false
	}
	rule := RulePermissionDenied
	if strings.Contains(text, "classifier") {
		rule = RuleClassifierBlocked
	}
	return Flag{
		Rule:      rule,
		Severity:  SeverityMedium,
		At:        at,
		Detail:    excerpt(text),
		ToolUseID: b.ToolUseID,
	}, true
}

// blockText reads a content payload that is either a bare string or an array of
// blocks, which is how a tool_result's content arrives depending on whether the
// tool returned text or something structured.
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var b strings.Builder
	for _, it := range items {
		if it.Type == "text" {
			b.WriteString(it.Text)
		}
	}
	return b.String()
}

// excerpt turns a log-derived string into the one bounded line a flag carries.
//
// Order matters: redact first, then collapse, then cut. Cutting first can leave
// half a secret in the excerpt, and collapsing first would join a value to the
// next word.
func excerpt(s string) string {
	s = redactSecrets(s)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= flagDetailRunes {
		return s
	}
	return string(r[:flagDetailRunes]) + "..."
}

// secretNames are the assignment targets whose value is replaced before a
// command is shown. A flag excerpt is written to the API and to `-l -json`, and
// `GITHUB_TOKEN=ghp_… gh api …` is a command an agent writes.
//
// Best-effort by construction: it recognises the `NAME=value` shape and nothing
// else, so a secret passed as a positional argument still shows. It is a
// courtesy, not a control -- the log it came from holds the value in the clear
// either way.
var secretNames = []string{
	"token", "secret", "password", "passwd", "apikey", "api_key", "accesskey",
	"access_key", "credential", "private_key", "auth",
}

// redactSecrets replaces the value of any `NAME=value` whose name looks like a
// secret. The name is matched case-insensitively on a substring, so
// GITHUB_TOKEN, npm_config_authToken and MY_API_KEY_2 are all covered.
func redactSecrets(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			b.WriteString(s[i:])
			break
		}
		eq += i
		nameStart := wordStart(s, eq)
		valueEnd := wordEnd(s, eq+1)
		if looksSecret(s[nameStart:eq]) && valueEnd > eq+1 {
			b.WriteString(s[i : eq+1])
			b.WriteString("[redacted]")
		} else {
			b.WriteString(s[i:valueEnd])
		}
		i = valueEnd
	}
	return b.String()
}

// wordStart walks back from an '=' to the start of the name in front of it.
func wordStart(s string, eq int) int {
	i := eq
	for i > 0 && !isShellBreak(s[i-1]) && s[i-1] != '=' {
		i--
	}
	return i
}

// wordEnd walks forward to the end of a value, honouring quotes so a quoted
// secret containing a space is redacted whole.
func wordEnd(s string, i int) int {
	if i < len(s) && (s[i] == '\'' || s[i] == '"') {
		q := s[i]
		if j := strings.IndexByte(s[i+1:], q); j >= 0 {
			return i + 1 + j + 1
		}
		return len(s)
	}
	for i < len(s) && !isShellBreak(s[i]) {
		i++
	}
	return i
}

func looksSecret(name string) bool {
	lower := strings.ToLower(name)
	for _, n := range secretNames {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}
