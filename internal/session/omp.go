package session

// This file is the second session producer. It reads Oh My Pi's session store
// the way session.go reads Claude Code's, and hands Discover the same
// []Session so ui, web and jump stay unaware that there is more than one agent.
//
// The two stores share only the fact that they are JSONL. omp's format is
// documented in its own docs/session.md; the parts that matter here:
//
//   - The physical first line is a fixed-width 256-byte `{"type":"title"}` slot,
//     so a rename does not rewrite the file. The logical header follows it.
//   - Entries form an append-only tree (`id`/`parentId`) with a mutable leaf
//     pointer, not a linear log. See the ceiling noted on parseOMPLogFile.
//   - A tool result is its own message role ("toolResult"), not a user turn
//     carrying tool_result blocks, and every tool call is announced by a
//     `tool_execution_start` custom entry that carries its toolCallId. Pending
//     tool calls are therefore an exact set difference rather than a count.
//   - There are no progress heartbeats. The tool markers take their place.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ompSessionsDirEnv overrides where csm looks for omp session logs.
//
// omp relocates its store for `--profile` and `--session-dir`, so unlike
// Claude Code's projects directory the default path is not a guarantee. A csm
// that only knew the default would report nothing at all while sessions ran,
// which is the failure mode this package works hardest to avoid.
const ompSessionsDirEnv = "CSM_OMP_SESSIONS_DIR"

// OMPSessionsDir returns the directory omp keeps session logs in.
func OMPSessionsDir() (string, error) {
	if dir := os.Getenv(ompSessionsDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".omp", "agent", "sessions"), nil
}

// ompSessionIDFromLogFile returns the session id from a log file path. omp names
// each log "<timestamp>_<uuid>.jsonl": the timestamp prefix is what makes the
// directory sort by age, and the uuid is the session id that `--resume` matches.
func ompSessionIDFromLogFile(logFile string) string {
	stem := strings.TrimSuffix(filepath.Base(logFile), ".jsonl")
	if _, id, ok := strings.Cut(stem, "_"); ok && id != "" {
		return id
	}
	return stem
}

// ompEntry is one line of an omp session JSONL. Only the fields csm reads are
// declared: omp's entry union is far wider, and an unrecognised type is skipped
// rather than treated as a corrupt file.
type ompEntry struct {
	Type       string      `json:"type"`
	Timestamp  time.Time   `json:"timestamp"`
	CustomType string      `json:"customType,omitempty"`
	Message    *ompMessage `json:"message,omitempty"`
	// Data is the payload of a custom entry. omp types it as unknown and its
	// shape depends on customType, so it arrives raw -- decoding it eagerly
	// would fail an entire line over a payload csm does not look at -- and
	// decodeOMPEntry drops it once the parts csm reads are decoded.
	Data json.RawMessage `json:"data,omitempty"`

	// Header (type "session") and title-slot (type "title") fields. ID is the
	// session id on the header and an 8-char entry id everywhere else, so it is
	// only ever read from the header.
	CWD   string `json:"cwd,omitempty"`
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`

	// toolStart is Data decoded, for a tool_execution_start entry.
	toolStart *ompToolStart
}

// ompMessage is the message omp stores on a "message" entry. Roles are "user",
// "assistant" and "toolResult".
type ompMessage struct {
	Role       string `json:"role,omitempty"`
	Model      string `json:"model,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	// Content is raw because omp allows either a block array or a bare string
	// (extension-provided messages use the latter). decodeOMPEntry turns it into
	// Text and clears it.
	Content json.RawMessage `json:"content,omitempty"`
	// Text is the decoded display text.
	Text string `json:"-"`
}

// ompContentItem is one block of a message's content array. omp emits "text",
// "thinking" and "toolCall" blocks; only text is displayed.
type ompContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// decodeOMPEntry decodes the raw payloads csm reads and drops the raw copies.
//
// It runs once, at parse time, because determineOMPStatus and the task label run
// on every tick *including* parse-cache hits: decoding there meant re-parsing
// every tool_execution_start payload and the newest assistant's content blocks
// twice a second for the lifetime of a session. Claude Code's path pre-decodes
// in Message.UnmarshalJSON for the same reason. Clearing the raw fields also
// keeps the cached entries from holding a second copy of every message body.
//
// A custom payload csm does not read is discarded rather than kept: nothing
// downstream looks at it, and a future reader should decode it here too.
func decodeOMPEntry(e *ompEntry) {
	if e.Message != nil {
		e.Message.Text = ompContentText(e.Message.Content)
		e.Message.Content = nil
	}
	if e.Type == "custom" && e.CustomType == "tool_execution_start" && len(e.Data) > 0 {
		var start ompToolStart
		if json.Unmarshal(e.Data, &start) == nil {
			e.toolStart = &start
		}
	}
	e.Data = nil
}

// ompContentText returns a message's displayable text: its text blocks joined,
// or the whole content when it is a bare string. Thinking and tool-call blocks
// are skipped -- thinking is not a message to the user, and a tool call is
// reported through the status column instead.
func ompContentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var blocks []ompContentItem
	if err := json.Unmarshal(content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	return ""
}

// ompToolStart is the data of a "tool_execution_start" custom entry, which omp
// writes immediately before a tool implementation runs. Intent is a one-line
// description of why the tool is being called, which makes a better task label
// than the tool name alone.
type ompToolStart struct {
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Intent     string `json:"intent"`
}

// ompParsedLog holds everything one pass over an omp log yields. As with
// parsedLog, these fields change only when the file does, so they are safe to
// cache against its (modTime, size).
type ompParsedLog struct {
	entries       []ompEntry
	cwd           string
	title         string
	sessionID     string
	lastMessage   string
	model         string
	lastEntryTime time.Time
}

// parseOMPLogFile scans an omp session log once and extracts what the live view
// needs, keeping the last `keep` entries for the status rules.
//
// Known ceiling: omp sessions are trees, and the physical tail of the file is
// not necessarily on the live branch after a rewind or a branch switch. csm
// reads the tail anyway. It is what the file's mtime reflects, and walking
// parentId from the leaf would mean reading whole multi-MB files on every tick
// to correct a status that is wrong only in the seconds after a rewind.
func parseOMPLogFile(logFile string, keep int) (ompParsedLog, error) {
	return parseOMPLogFileWithLimit(logFile, keep, maxLogLineBytes)
}

// parseOMPLogFileWithLimit is parseOMPLogFile with the scanner's max-line-size
// made explicit, so tests can reproduce an oversized-line scan error without
// allocating a real maxLogLineBytes-sized line.
func parseOMPLogFileWithLimit(logFile string, keep int, maxLineBytes int) (ompParsedLog, error) {
	file, err := os.Open(logFile)
	if err != nil {
		return ompParsedLog{}, err
	}
	defer func() { _ = file.Close() }()

	var pl ompParsedLog
	var entries []ompEntry

	scanner := newLogScanner(file, maxLineBytes)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry ompEntry
		// A line omp wrote that csm cannot parse is one entry lost, not a
		// broken session: omp's own loader is lenient here for the same reason.
		if json.Unmarshal(line, &entry) != nil {
			continue
		}

		switch entry.Type {
		case "title":
			// The fixed-width title slot, not part of the logical entry stream.
			if entry.Title != "" {
				pl.title = entry.Title
			}
			continue
		case "session":
			pl.cwd, pl.sessionID = entry.CWD, entry.ID
			// The slot is the copy omp rewrites on rename; the header keeps the
			// original. Letting the header win showed a stale title whenever the
			// rename was recorded outside the kept tail -- two files on the
			// machine this was written on already disagree.
			if pl.title == "" && entry.Title != "" {
				pl.title = entry.Title
			}
			continue
		case "title_change":
			// A rename updates the slot, but an older file may carry only the
			// audit entry; last one wins.
			if entry.Title != "" {
				pl.title = entry.Title
			}
			continue
		}

		decodeOMPEntry(&entry)
		entries = append(entries, entry)
	}

	if len(entries) > keep {
		entries = entries[len(entries)-keep:]
	}
	pl.entries = entries
	pl.lastMessage, pl.model = ompLastAssistant(entries)
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].Timestamp.IsZero() {
			pl.lastEntryTime = entries[i].Timestamp
			break
		}
	}

	return pl, scanner.Err()
}

// ompLastAssistant returns the newest assistant message's text and the model
// that produced it. The model comes from the newest assistant message even when
// that message has no text of its own (a turn that only called tools).
func ompLastAssistant(entries []ompEntry) (text, model string) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type != "message" || e.Message == nil || e.Message.Role != "assistant" {
			continue
		}
		if model == "" {
			model = e.Message.Model
		}
		if t := e.Message.Text; t != "" {
			return t, model
		}
	}
	return "", model
}

// discoverOMP returns the omp sessions worth showing, given the process scan
// Discover already made. liveFiles collects the logs it parsed so the caller can
// prune the parse cache.
//
// A missing sessions directory is not an error: most users run only one of the
// two agents. A directory that exists but cannot be read is skipped rather than
// failing the sweep, because taking the Claude half of the dashboard down over
// an unreadable ~/.omp is the worse outcome. That trade is a known ceiling: the
// scan reports no omp sessions and no reason.
func discoverOMP(procs []harnessProcess, liveFiles map[string]struct{}) []Session {
	sessionsDir, err := OMPSessionsDir()
	if err != nil {
		return nil
	}
	buckets, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}

	// omp buckets logs by its own encoding of the working directory, so the
	// processes are keyed by the raw cwd and joined to a bucket through the cwd
	// recorded in that bucket's newest log.
	pidsByCWD := pidsByDir(procs, HarnessOMP, func(cwd string) string { return cwd })
	procByPID := procsByPID(procs)
	// Breadcrumbs only matter for pairing a log to a pid, so with no omp process
	// running there is nothing for them to decide and the directory read is pure
	// cost on every tick.
	var breadcrumbs ompBreadcrumbs
	if len(pidsByCWD) > 0 {
		breadcrumbs = ompTerminalSessions()
	}

	var sessions []Session
	for _, bucket := range buckets {
		if !bucket.IsDir() || strings.HasPrefix(bucket.Name(), ".") {
			continue
		}
		dir := filepath.Join(sessionsDir, bucket.Name())

		// One directory scan. The newest log names the working directory this
		// bucket belongs to, which is what says how many processes are running
		// in it -- and only then can the selection know how many logs count as
		// candidates. Reading the cwd through the parse cache means the header
		// costs nothing: it is the same entry the full parse below reuses.
		logs, err := listLogsByRecency(dir)
		if err != nil || len(logs) == 0 {
			continue
		}
		newest := logs[0]
		header, _ := cachedParseOMPLogFile(newest.path, newest.modTime, newest.size)
		cwd := header.cwd
		pids := pidsByCWD[cwd]

		for _, logFile := range selectActiveLogs(logs, len(pids)) {
			liveFiles[logFile] = struct{}{}

			isRunning, pid, pidConfident := pairOMPProcess(logFile, pids, breadcrumbs, procByPID)
			// Orphan-ness follows whichever pid the pairing settled on; with no
			// pid the session cannot be a ghost, which is the safe side.
			orphaned := procByPID[pid].orphan

			session, err := parseOMPSession(bucket.Name(), cwd, logFile, isRunning, pid, orphaned)
			if err != nil {
				continue
			}
			session.PIDConfident = pidConfident && session.GhostPID > 0

			sessions = append(sessions, session)
		}
	}

	return sessions
}

// ompProjectName labels a session.
//
// The cwd from the log header is authoritative. When it could not be read, the
// bucket name is shown verbatim rather than decoded: omp's bucket encoding is
// lossy -- a dash inside a directory name is indistinguishable from a separator
// -- and decodeProjectName is Claude Code's decoder, which finds none of its
// markers in a home-relative omp bucket and turns
// `-Projects-personal-claude-sessions-monitor` into
// `Projects/personal/claude/sessions/monitor`. A verbatim bucket name is at
// least true.
func ompProjectName(cwd, bucketName string) string {
	if cwd != "" {
		return extractProjectName(cwd)
	}
	return strings.TrimPrefix(bucketName, "-")
}

// parseOMPSession parses an omp session from its log file. cwd is the working
// directory discoverOMP resolved for the bucket, passed in so a session whose
// own log cannot be read still carries a real project name and a directory jump
// can use.
func parseOMPSession(bucketName, cwd, logFile string, isRunning bool, pid int,
	orphaned bool) (Session, error) {
	session := Session{
		Project:     ompProjectName(cwd, bucketName),
		CWD:         cwd,
		LogFile:     logFile,
		Status:      StatusInactive,
		ProjectPath: bucketName,
		SessionID:   ompSessionIDFromLogFile(logFile),
		Harness:     HarnessOMP,
	}

	if cached, ok := LoadOrigin(session.SessionID); ok {
		session.Origin = cached
	} else if isRunning && pid > 0 {
		if detected := DetectOrigin(pid); !detected.IsZero() {
			session.Origin = detected
			_ = SaveOrigin(session.SessionID, detected)
		}
	}

	info, err := os.Stat(logFile)
	if err != nil {
		return session, err
	}
	session.LastActivity = info.ModTime()

	pl, err := cachedParseOMPLogFile(logFile, info.ModTime(), info.Size())
	if err != nil {
		session.Degraded = err.Error()
	}

	applyOMPParsedLog(&session, pl, isRunning, pid, orphaned, info.ModTime())
	return session, nil
}

// applyOMPParsedLog populates a Session from an ompParsedLog. As with
// applyParsedLog, the file-derived fields come straight from pl while the status
// and pid fields are recomputed every call, because they depend on wall-clock
// time and the running-process set.
//
// Deliberately left zero: ContextPercent, ContextTokens, ContextWindow,
// GitBranch, HasUnsandboxed and Subagents. omp is multi-provider, so a context
// window cannot be derived from the model id the way it can for Claude, and a
// percentage that is wrong is worse than a column that is blank. The other
// three have no equivalent in omp's log at all.
func applyOMPParsedLog(session *Session, pl ompParsedLog, isRunning bool, pid int, orphaned bool,
	fileModTime time.Time) {
	if pl.cwd != "" {
		session.Project = extractProjectName(pl.cwd)
		session.CWD = pl.cwd
	}
	if pl.title != "" {
		session.SessionTitle = pl.title
	}
	if pl.sessionID != "" {
		session.SessionID = pl.sessionID
	}
	if !pl.lastEntryTime.IsZero() {
		session.LastActivity = pl.lastEntryTime
	}
	session.LastMessage = pl.lastMessage
	session.Model = pl.model

	if isRunning && pid > 0 {
		session.GhostPID = pid
	}
	session.Status, session.Task = determineOMPStatus(pl.entries, isRunning, fileModTime)
	session.IsGhost = isRunning && orphaned && time.Since(session.LastActivity) > GhostThreshold
}

// determineOMPStatus decides an omp session's status, using the same four
// values and the same three time windows as determineStatus. The evidence
// differs; the meaning of each answer must not.
//
// The rules, in evaluation order:
//
//   - Not running -> Inactive. Running with no entries -> Waiting (just started).
//   - A tool call announced but not answered: younger than recentActivityWindow
//     -> Working, older -> Needs Input. This is the only producer of Needs Input,
//     and unlike Claude Code the match is exact -- both the announcement and the
//     result carry the same toolCallId.
//   - A session_exit newer than the last message -> the session recorded its own
//     shutdown -> Waiting.
//   - The last assistant message finished its turn (stopReason "stop" or
//     "aborted") with no newer user prompt -> Waiting.
//   - The log was written within logWriteWindow -> Working.
//   - Nothing at all within sessionStaleWindow -> Waiting.
//   - A recent assistant message or user prompt -> Working.
//   - Otherwise Waiting.
func determineOMPStatus(entries []ompEntry, isRunning bool, fileModTime time.Time) (Status, string) {
	if !isRunning {
		return StatusInactive, "-"
	}
	if len(entries) == 0 {
		// Process running but nothing logged: a session in its first moments.
		// omp keeps a new session in memory until its first assistant message,
		// so this window is wider than Claude Code's.
		return StatusWaiting, "-"
	}

	var lastAssistant, lastUser, lastExit *ompEntry
	var lastTimestamp time.Time
	for i := len(entries) - 1; i >= 0; i-- {
		e := &entries[i]
		if !e.Timestamp.IsZero() && e.Timestamp.After(lastTimestamp) {
			lastTimestamp = e.Timestamp
		}
		switch {
		case e.Type == "message" && e.Message != nil && e.Message.Role == "assistant":
			if lastAssistant == nil {
				lastAssistant = e
			}
		case e.Type == "message" && e.Message != nil && e.Message.Role == "user":
			if lastUser == nil {
				lastUser = e
			}
		case e.Type == "custom" && e.CustomType == "session_exit":
			if lastExit == nil {
				lastExit = e
			}
		}
	}

	// A tool call with no result is the strongest signal available, and it is
	// checked first for the same reason Claude Code's pending tool_use is: a
	// session waiting on an approval must not be aged out into Waiting.
	if pending, ok := ompPendingTool(entries); ok {
		task := "Using: " + pending.ToolName
		if pending.Intent != "" {
			task = pending.Intent
		}
		if time.Since(pending.at) < recentActivityWindow {
			return StatusWorking, task
		}
		return StatusNeedsInput, task
	}

	// omp records its own shutdown, including an abnormal one. A process still
	// running past that point is between sessions, not working.
	if lastExit != nil && (lastAssistant == nil || lastExit.Timestamp.After(lastAssistant.Timestamp)) {
		if lastUser == nil || !lastUser.Timestamp.After(lastExit.Timestamp) {
			return StatusWaiting, "-"
		}
	}

	// stopReason "toolUse" means the turn continues and is covered by the
	// pending-tool check above; "stop" and "aborted" both end it.
	if lastAssistant != nil && lastAssistant.Message != nil {
		switch lastAssistant.Message.StopReason {
		case "stop", "aborted":
			if lastUser == nil || !lastUser.Timestamp.After(lastAssistant.Timestamp) {
				return StatusWaiting, "-"
			}
		}
	}

	// The remaining rules are the same on both sides; ageStatus holds them.
	// omp gives tool results their own role, so a "user" message is always a
	// real prompt -- no isUserPrompt equivalent to write.
	var lastPrompt time.Time
	if lastUser != nil && (lastAssistant == nil || lastUser.Timestamp.After(lastAssistant.Timestamp)) {
		lastPrompt = lastUser.Timestamp
	}
	var lastAssistantAt time.Time
	if lastAssistant != nil {
		lastAssistantAt = lastAssistant.Timestamp
	}

	return ageStatus(activity{
		fileModTime:   fileModTime,
		lastEntry:     lastTimestamp,
		lastAssistant: lastAssistantAt,
		lastPrompt:    lastPrompt,
		task:          func() string { return ompTask(lastAssistant) },
	})
}

// ompPendingToolCall is a tool omp announced and has not recorded a result for.
type ompPendingToolCall struct {
	ompToolStart
	at time.Time
}

// ompPendingTool returns the oldest unanswered tool call, if any.
//
// Every announcement and every result carries the same toolCallId, so this is a
// set difference rather than Claude Code's count of tool_use blocks against
// tool_result blocks -- which cannot tell which call is outstanding when a turn
// makes several. The oldest is the one to report: it is what the session is
// actually blocked on.
func ompPendingTool(entries []ompEntry) (ompPendingToolCall, bool) {
	answered := make(map[string]bool)
	for _, e := range entries {
		if e.Type == "message" && e.Message != nil && e.Message.Role == "toolResult" &&
			e.Message.ToolCallID != "" {
			answered[e.Message.ToolCallID] = true
		}
	}

	for _, e := range entries {
		if e.toolStart == nil {
			continue
		}
		if e.toolStart.ToolCallID == "" || answered[e.toolStart.ToolCallID] {
			continue
		}
		return ompPendingToolCall{ompToolStart: *e.toolStart, at: e.Timestamp}, true
	}

	return ompPendingToolCall{}, false
}

// ompTask describes what a session is doing, from its newest assistant message.
func ompTask(lastAssistant *ompEntry) string {
	if lastAssistant == nil || lastAssistant.Message == nil {
		return "-"
	}
	return taskLabel(lastAssistant.Message.Text)
}
