package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Status represents the current state of a Claude session
type Status string

const (
	StatusWorking    Status = "Working"
	StatusNeedsInput Status = "Needs Input"
	StatusWaiting    Status = "Waiting"
	StatusInactive   Status = "Inactive"
)

// Session represents a Claude Code session
type Session struct {
	Project        string     `json:"project"`
	Status         Status     `json:"status"`
	LastActivity   time.Time  `json:"last_activity"`
	Task           string     `json:"task"`
	Summary        string     `json:"summary,omitempty"`
	LastMessage    string     `json:"last_message,omitempty"`
	LogFile        string     `json:"log_file"`
	ProjectPath    string     `json:"-"`                         // Encoded project directory name (as used under ~/.claude/projects)
	CWD            string     `json:"-"`                         // Absolute working directory recorded in the log
	SessionID      string     `json:"session_id,omitempty"`      // Claude session UUID (log filename stem)
	Origin         Origin     `json:"origin,omitempty"`          // Where the session was launched from
	IsGhost        bool       `json:"is_ghost,omitempty"`        // True if process running but log is stale
	GhostPID       int        `json:"ghost_pid,omitempty"`       // PID of the ghost process (for killing)
	PIDConfident   bool       `json:"-"`                         // True when GhostPID is certainly this session's process, not a positional guess
	GitBranch      string     `json:"git_branch,omitempty"`      // Current git branch
	HasUnsandboxed bool       `json:"has_unsandboxed,omitempty"` // True if any command bypassed sandbox
	ContextPercent float64    `json:"context_percent,omitempty"` // Percentage of context window used
	ContextTokens  int        `json:"context_tokens,omitempty"`  // Total input tokens from last usage entry
	Model          string     `json:"model,omitempty"`           // Model id from the latest assistant usage (e.g. "claude-opus-4-7")
	SessionTitle   string     `json:"session_title,omitempty"`   // Custom title set by user/Claude
	Subagents      []Subagent `json:"subagents,omitempty"`       // Live subagents spawned by this session
}

// RunningProcess represents a Claude process with its PID and working directory
type RunningProcess struct {
	PID int
	Dir string // Encoded directory name
}

// LogEntry represents a single line in the JSONL log
type LogEntry struct {
	Type        string    `json:"type"`
	Subtype     string    `json:"subtype,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Message     *Message  `json:"message,omitempty"`
	Summary     string    `json:"summary,omitempty"` // For type: "summary" entries
	GitBranch   string    `json:"gitBranch,omitempty"`
	CWD         string    `json:"cwd,omitempty"`         // Working directory of the Claude process
	CustomTitle string    `json:"customTitle,omitempty"` // User/Claude-set session title
}

// Message represents the message field in a log entry
type Message struct {
	Role       string        `json:"role,omitempty"`
	Model      string        `json:"model,omitempty"`
	Content    []ContentItem `json:"-"`
	Usage      *Usage        `json:"usage,omitempty"`
	StopReason string        `json:"stop_reason,omitempty"`

	// RawContent holds the unparsed content array for custom unmarshalling.
	// Content arrays can contain either objects ({"type":"text",...}) or
	// bare strings (individual characters of user prompts).
	RawContent json.RawMessage `json:"content,omitempty"`
}

// MarshalJSON writes Content as the "content" field so that round-tripping works.
func (m Message) MarshalJSON() ([]byte, error) {
	type Alias Message
	aux := struct {
		Alias
		Content []ContentItem `json:"content,omitempty"`
	}{
		Alias:   (Alias)(m),
		Content: m.Content,
	}
	// Clear RawContent so it doesn't double-write
	aux.RawContent = nil
	return json.Marshal(aux)
}

// UnmarshalJSON handles mixed content arrays where elements can be
// either ContentItem objects or bare strings (user prompt characters).
func (m *Message) UnmarshalJSON(data []byte) error {
	// Use an alias to avoid infinite recursion
	type Alias Message
	aux := &struct {
		*Alias
	}{Alias: (*Alias)(m)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	if len(m.RawContent) == 0 {
		return nil
	}

	// Content can be either a plain string (user prompts) or an array
	// of ContentItem objects (possibly mixed with bare strings).
	if len(m.RawContent) > 0 && m.RawContent[0] == '"' {
		var s string
		if json.Unmarshal(m.RawContent, &s) == nil && s != "" {
			m.Content = []ContentItem{{Type: "text", Text: s}}
		}
		return nil
	}

	// Parse as array, handling both object and string elements
	var rawItems []json.RawMessage
	if err := json.Unmarshal(m.RawContent, &rawItems); err != nil {
		return nil
	}

	var items []ContentItem
	var textBuf strings.Builder

	flushText := func() {
		if textBuf.Len() > 0 {
			items = append(items, ContentItem{Type: "text", Text: textBuf.String()})
			textBuf.Reset()
		}
	}

	for _, raw := range rawItems {
		// Check if this element is a bare string
		var s string
		if json.Unmarshal(raw, &s) == nil && len(raw) > 0 && raw[0] == '"' {
			textBuf.WriteString(s)
			continue
		}

		// Otherwise parse as a ContentItem object
		flushText()
		var item ContentItem
		if json.Unmarshal(raw, &item) == nil {
			items = append(items, item)
		}
	}
	flushText()

	m.Content = items
	return nil
}

// Usage represents token usage data from the API response
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

// ContentItem represents an item in the content array
type ContentItem struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Name      string          `json:"name,omitempty"`        // For tool_use
	ID        string          `json:"id,omitempty"`          // For tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // For tool_result
	Input     json.RawMessage `json:"input,omitempty"`       // For tool_use inputs
}

// BashToolInput represents the input for a Bash tool_use entry
type BashToolInput struct {
	Command                   string `json:"command"`
	DangerouslyDisableSandbox bool   `json:"dangerouslyDisableSandbox"`
}

// ClaudeProjectsDir returns the path to the Claude projects directory
func ClaudeProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// getRunningClaudeDirs returns a map of encoded directory names to PIDs where Claude processes are running
// The keys are in the same format as the project directory names (e.g., -Users-username-Projects-...)
// Multiple Claude processes in the same directory are tracked as separate PIDs.
func getRunningClaudeDirs() map[string][]int {
	dirs := make(map[string][]int)

	// Use ps directly without a shell pipeline to avoid shell injection risks
	cmd := exec.Command("ps", "ax", "-o", "pid=,comm=")
	output, err := cmd.Output()
	if err != nil {
		return dirs
	}

	// Parse ps output to find claude processes
	for _, line := range bytes.Split(output, []byte("\n")) {
		fields := bytes.Fields(line)
		if len(fields) < 2 {
			continue
		}
		comm := string(fields[len(fields)-1])
		if !strings.HasSuffix(comm, "claude") {
			continue
		}

		pidStr := string(fields[0])
		pid := 0
		fmt.Sscanf(pidStr, "%d", &pid)
		if pid == 0 {
			continue
		}

		// Get cwd for each process
		path, err := getProcessCwd(pid)
		if err != nil || path == "" {
			continue
		}
		// Convert to encoded format (same as project directory names)
		encoded := encodeProjectPath(path)
		dirs[encoded] = append(dirs[encoded], pid)
	}

	return dirs
}

// getProcessCwd returns the current working directory of a process by PID.
// On Linux it reads /proc/<pid>/cwd; on Darwin it uses lsof.
// Note: on Linux, reading /proc/<pid>/cwd requires the caller to be the same
// user as the target process (or root). If csm runs as a different user,
// os.Readlink will return a permission error and the process will be skipped.
func getProcessCwd(pid int) (string, error) {
	if runtime.GOOS == "linux" {
		return os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	}

	// Darwin: use lsof to find cwd
	pidStr := fmt.Sprintf("%d", pid)
	lsofCmd := exec.Command("lsof", "-p", pidStr)
	lsofOutput, err := lsofCmd.Output()
	if err != nil {
		return "", err
	}

	lines := bytes.Split(lsofOutput, []byte("\n"))
	for _, l := range lines {
		if bytes.Contains(l, []byte(" cwd ")) {
			lFields := bytes.Fields(l)
			if len(lFields) >= 9 {
				return string(lFields[len(lFields)-1]), nil
			}
		}
	}
	return "", fmt.Errorf("cwd not found in lsof output for pid %d", pid)
}

// sessionIDFromLogFile returns the session UUID from a log file path.
// Claude Code names each session log "<uuid>.jsonl" so the stem is the session id.
func sessionIDFromLogFile(logFile string) string {
	base := filepath.Base(logFile)
	return strings.TrimSuffix(base, ".jsonl")
}

// encodeProjectPath converts a filesystem path to the encoded directory name format
// used for the per-project folders under ~/.claude/projects.
//
// Claude Code replaces every character outside [A-Za-z0-9-] with a dash, so this
// must do the same rather than special-casing a few separators. Enumerating them
// silently breaks any path containing another character — e.g. a home directory
// like /home/user@corp.example, where an unencoded '@' makes the computed key
// miss the real directory and every session gets reported as inactive.
func encodeProjectPath(path string) string {
	// /Users/username/Projects/org/project -> -Users-username-Projects-org-project
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, path)
}

// Discover finds all active Claude sessions
func Discover() ([]Session, error) {
	// Serve a recent result if the TUI loop, SSE hub, and/or HTTP handlers are
	// all refreshing within the same tick.
	if cached, ok := cachedResult(); ok {
		return cached, nil
	}

	projectsDir, err := ClaudeProjectsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}

	// Get directories where Claude is currently running (TTL-cached to avoid
	// spawning ps/lsof on every refresh).
	runningDirs := cachedRunningClaudeDirs()

	var sessions []Session
	// Track the log files we actually parse this sweep so stale entries can be
	// evicted from the parse cache afterwards (see pruneParseCache).
	liveFiles := map[string]struct{}{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip hidden directories
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		projectDir := filepath.Join(projectsDir, entry.Name())
		pids := runningDirs[entry.Name()]

		logFiles, err := findActiveLogs(projectDir, len(pids))
		if err != nil || len(logFiles) == 0 {
			continue
		}

		for i, logFile := range logFiles {
			liveFiles[logFile] = struct{}{}

			// findActiveLogs already decided every file here is a plausible
			// candidate for one of this directory's runningCount processes, but
			// pairing a *specific* pid to a *specific* file by array position
			// (most-recent log <-> first ps result) has no real correspondence --
			// neither list is ordered by anything that ties them together. When
			// a directory holds more candidate logs than confidently-paired
			// pids (i >= len(pids)), still treat the session as running rather
			// than defaulting it to not-running: a session that's actually
			// closed just gets correctly demoted to Waiting by its own content
			// staleness, whereas the reverse -- a genuinely active session
			// getting marked not-running because it lost a positional pairing
			// it was never guaranteed to win -- surfaces as that session
			// wrongly showing Inactive. Only carry a specific pid through
			// (for GhostPID / --kill-ghosts) when the pairing is one we're
			// actually confident in.
			isRunning := len(pids) > 0
			pid := 0
			if i < len(pids) {
				pid = pids[i]
			}
			// The pairing above is positional, so it only actually identifies a
			// process when there's exactly one candidate on each side. Anything
			// that needs to be *right* about which process belongs to this
			// session (rather than merely "some pid for this directory", which
			// is all --kill-ghosts needs) must check this first.
			pidConfident := len(pids) == 1 && len(logFiles) == 1

			session, err := parseSession(entry.Name(), logFile, isRunning, pid)
			if err != nil {
				continue
			}
			session.PIDConfident = pidConfident && session.GhostPID > 0

			sessions = append(sessions, session)
		}
	}

	// Evict parse-cache entries for logs no longer in the active set, keeping the
	// cache bounded to the current working set over a long-running server.
	pruneParseCache(liveFiles)

	// Sort by status priority, then by last activity
	sort.Slice(sessions, func(i, j int) bool {
		return sessionLess(sessions[i], sessions[j])
	})

	storeResult(sessions)
	return sessions, nil
}

// sessionLess orders sessions by status priority, then by last activity.
//
// Working sessions are the exception: renderSessionRow always displays "Now"
// for them regardless of the actual LastActivity, so sorting by that
// timestamp swaps their rows on nothing the user can see — each session's
// log picks up new entries at slightly different real-world moments, and
// that jitter alone reorders the list every refresh. Break ties among
// Working sessions by project then session ID instead, both fixed for the
// session's lifetime, so those rows hold a stable order.
func sessionLess(a, b Session) bool {
	pa, pb := statusPriority(a.Status), statusPriority(b.Status)
	if pa != pb {
		return pa < pb
	}
	if a.Status == StatusWorking && b.Status == StatusWorking {
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.SessionID < b.SessionID
	}
	return a.LastActivity.After(b.LastActivity)
}

// statusPriority returns the sort priority for a status (lower = higher priority)
func statusPriority(s Status) int {
	switch s {
	case StatusWorking:
		return 0
	case StatusNeedsInput:
		return 1
	case StatusWaiting:
		return 2
	case StatusInactive:
		return 3
	default:
		return 4
	}
}

// activeLogFreshnessWindow is the second-chance window for a log file that
// didn't make the top-runningCount cut by recency. There is no reliable way
// to attribute a specific running pid to a specific log file in a directory
// with more than one (Claude Code exposes no pid/session correlation), so a
// genuinely active session's log can lose that race to an unrelated, merely
// fresher file in the same directory -- most commonly when it goes quiet for
// a while for a mundane reason (extended thinking, a long tool call, the user
// stepping away mid-turn). Losing the race then meant the log was dropped
// entirely: not shown with a wrong status, just never considered at all.
// 30 minutes comfortably covers any of those normal quiet periods while still
// excluding logs from sessions that ended hours or days ago.
const activeLogFreshnessWindow = 30 * time.Minute

// findActiveLogs returns all active JSONL log files for a project directory.
// If runningCount > 0, returns at least that many files (the most recently
// modified), plus any additional files modified within activeLogFreshnessWindow.
// If runningCount == 0, returns only the single most recent file.
func findActiveLogs(dir string, runningCount int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type logEntry struct {
		path    string
		modTime time.Time
		size    int64
	}

	var logs []logEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		// Skip agent files (subagents) - only track main sessions
		if strings.HasPrefix(entry.Name(), "agent-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		logs = append(logs, logEntry{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
			size:    info.Size(),
		})
	}

	if len(logs) == 0 {
		return nil, nil
	}

	// Sort by modification time, newest first
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].modTime.After(logs[j].modTime)
	})

	if runningCount == 0 {
		// No running processes: return only the most recent file
		// Prefer newest non-empty, but return empty if it's newer (fresh session)
		for _, l := range logs {
			if l.size > 0 {
				// Check if there's an even newer empty file
				if logs[0].size == 0 && logs[0].modTime.After(l.modTime) {
					return []string{logs[0].path}, nil
				}
				return []string{l.path}, nil
			}
		}
		// All empty, return newest
		return []string{logs[0].path}, nil
	}

	// Running processes: collect active logs
	recentThreshold := time.Now().Add(-activeLogFreshnessWindow)
	seen := make(map[string]bool)
	var result []string

	// Include the top runningCount files (paired with running processes)
	for i := 0; i < len(logs) && i < runningCount; i++ {
		result = append(result, logs[i].path)
		seen[logs[i].path] = true
	}

	// Also include any additional recently modified files
	for _, l := range logs {
		if !seen[l.path] && l.modTime.After(recentThreshold) {
			result = append(result, l.path)
		}
	}

	return result, nil
}

// parsedLog holds everything a single pass over a JSONL log file yields.
// These fields only change when the file itself changes, so they are safe to
// cache against the file's (modTime, size); the time-relative status is derived
// separately on every call (see applyParsedLog).
type parsedLog struct {
	entries        []LogEntry // last N full JSON entries
	summary        string
	cwd            string
	title          string
	lastMessage    string
	gitBranch      string
	hasUnsandboxed bool
	contextPercent float64
	contextTokens  int
	model          string
	// lastEntryTime is the most recent non-zero entry timestamp, used as
	// LastActivity when present (falls back to file modTime otherwise).
	lastEntryTime time.Time
}

// parseLogFile scans a JSONL log file exactly once and extracts every field the
// live view needs. It replaces three separate full-file passes (readLastEntries,
// QuickSessionStats, extractSummary) that parseSession previously made.
//
// maxLogLineBytes bounds a single JSONL line bufio.Scanner will accept before
// aborting the scan. The largest line observed in real logs is ~1.2MB, so
// 10MB leaves ample headroom without a raise that has no evidence behind it
// -- see cachedParseLogFile for why hitting this limit no longer means losing
// every entry already parsed before it.
const maxLogLineBytes = 10 * 1024 * 1024

// It keeps the last `keep` fully-parsed entries (for status/usage/message
// extraction, which need Message.Content and Usage), while capturing the
// early-file metadata (cwd, title) and the most recent summary in the same pass.
func parseLogFile(logFile string, keep int) (parsedLog, error) {
	return parseLogFileWithLimit(logFile, keep, maxLogLineBytes)
}

// parseLogFileWithLimit is parseLogFile with the scanner's max-line-size made
// an explicit parameter, so tests can reproduce an oversized-line scan error
// without allocating a real maxLogLineBytes-sized line.
func parseLogFileWithLimit(logFile string, keep int, maxLineBytes int) (parsedLog, error) {
	file, err := os.Open(logFile)
	if err != nil {
		return parsedLog{}, err
	}
	defer file.Close()

	var pl parsedLog
	var entries []LogEntry

	scanner := bufio.NewScanner(file)
	// The initial buffer's capacity must not exceed maxLineBytes: bufio.Scanner
	// only grows a token buffer when it needs to, so a capacity already bigger
	// than maxLineBytes would let a token past that limit through untouched.
	initialBufSize := 64 * 1024
	if maxLineBytes < initialBufSize {
		initialBufSize = maxLineBytes
	}
	buf := make([]byte, 0, initialBufSize)
	scanner.Buffer(buf, maxLineBytes)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Cheap string-prefix extraction for early-file metadata (avoids full
		// JSON parse). cwd stays constant within a session (first non-empty
		// wins); title can change (last non-empty wins).
		if pl.cwd == "" {
			if c := extractStringField(line, `"cwd":"`); c != "" {
				pl.cwd = c
			}
		}
		if t := extractStringField(line, `"customTitle":"`); t != "" {
			pl.title = t
		}

		// Most recent summary entry (summaries are cheap to detect first).
		if strings.Contains(line, `"type":"summary"`) {
			var entry LogEntry
			if json.Unmarshal([]byte(line), &entry) == nil &&
				entry.Type == "summary" && entry.Summary != "" {
				pl.summary = entry.Summary
			}
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}

	// Keep only the last N entries.
	if len(entries) > keep {
		entries = entries[len(entries)-keep:]
	}
	pl.entries = entries

	// Derive fields that only depend on the file contents.
	pl.lastMessage = extractLastAssistantMessage(entries)
	pl.gitBranch = extractGitBranch(entries)
	pl.hasUnsandboxed = detectUnsandboxedCommands(entries)
	pl.contextPercent, pl.contextTokens, pl.model = extractContextUsage(entries)
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].Timestamp.IsZero() {
			pl.lastEntryTime = entries[i].Timestamp
			break
		}
	}

	return pl, scanner.Err()
}

// parseSession parses a session from its log file
func parseSession(projectName, logFile string, isRunning bool, pid int) (Session, error) {
	session := Session{
		Project:     decodeProjectName(projectName),
		LogFile:     logFile,
		Status:      StatusInactive, // Default to inactive
		ProjectPath: projectName,    // Store the encoded name for matching
		SessionID:   sessionIDFromLogFile(logFile),
	}

	// Resolve the session's origin (terminal / IDE / Claude Desktop).
	// Historical sessions can only be classified if we previously cached them
	// while live, so we load from cache first and only detect when the process
	// is still running and no cache entry exists.
	if cached, ok := LoadOrigin(session.SessionID); ok {
		session.Origin = cached
	} else if isRunning && pid > 0 {
		if detected := DetectOrigin(pid); !detected.IsZero() {
			session.Origin = detected
			_ = SaveOrigin(session.SessionID, detected)
		}
	}

	// Get file modification time as fallback for last activity
	info, err := os.Stat(logFile)
	if err != nil {
		return session, err
	}
	session.LastActivity = info.ModTime()

	// Fetch the parsed log (single full-file pass), reusing the cache when the
	// file is unchanged since it was last parsed.
	pl, err := cachedParseLogFile(logFile, info.ModTime(), info.Size(), 100)
	if err != nil {
		return session, nil // Return with defaults
	}

	if len(pl.entries) == 0 {
		return session, nil
	}

	applyParsedLog(&session, pl, isRunning, pid, info.ModTime())
	return session, nil
}

// applyParsedLog populates a Session from a parsedLog. The file-derived fields
// come straight from pl (cacheable); the status and PID fields are recomputed
// on every call because they depend on wall-clock time and the running-process
// set, both of which change without the file changing.
func applyParsedLog(session *Session, pl parsedLog, isRunning bool, pid int, fileModTime time.Time) {
	if pl.cwd != "" {
		session.Project = extractProjectName(pl.cwd)
		session.CWD = pl.cwd
	}
	if pl.title != "" {
		session.SessionTitle = pl.title
	}
	session.Summary = pl.summary
	session.LastMessage = pl.lastMessage
	session.GitBranch = pl.gitBranch
	session.HasUnsandboxed = pl.hasUnsandboxed
	session.ContextPercent = pl.contextPercent
	session.ContextTokens = pl.contextTokens
	session.Model = pl.model

	// Time-relative + running-dependent: must be recomputed each call.
	session.Status, session.Task, session.IsGhost = determineStatus(pl.entries, isRunning, fileModTime)

	// A session that dispatched a subagent writes nothing to its own log until
	// the result comes back, so determineStatus sees a stale file and reports
	// Needs Input / Waiting. The subagent's log is where the work is visible.
	session.Subagents = discoverSubagents(session.LogFile, pendingToolUseIDs(pl.entries), isRunning)
	if len(session.Subagents) > 0 && rollUpSubagentStatus(session.Status) {
		session.Status = StatusWorking
		if session.Task == "" || session.Task == "-" {
			session.Task = subagentTask(session.Subagents)
		}
	}

	if isRunning && pid > 0 {
		session.GhostPID = pid
	}

	if !pl.lastEntryTime.IsZero() {
		session.LastActivity = pl.lastEntryTime
	}
}

// extractLastAssistantMessage extracts the last text message from an assistant entry
func extractLastAssistantMessage(entries []LogEntry) string {
	// Search from the end to find the most recent assistant message with text
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type != "assistant" || entry.Message == nil {
			continue
		}

		// Look for text content in the message
		for _, content := range entry.Message.Content {
			if content.Type == "text" && content.Text != "" {
				text := strings.TrimSpace(content.Text)
				if text == "" {
					continue
				}
				// Take first line only
				if idx := strings.Index(text, "\n"); idx > 0 {
					text = text[:idx]
				}
				// Clean up any leading markdown or formatting
				text = strings.TrimPrefix(text, "# ")
				text = strings.TrimPrefix(text, "## ")
				text = strings.TrimPrefix(text, "### ")
				return text
			}
		}
	}
	return ""
}

// extractGitBranch extracts the most recent git branch from entries
func extractGitBranch(entries []LogEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].GitBranch != "" {
			return entries[i].GitBranch
		}
	}
	return ""
}

// detectUnsandboxedCommands checks if any Bash commands ran with sandbox disabled
func detectUnsandboxedCommands(entries []LogEntry) bool {
	for _, entry := range entries {
		if entry.Type != "assistant" || entry.Message == nil {
			continue
		}
		for _, content := range entry.Message.Content {
			if content.Type == "tool_use" && content.Name == "Bash" && len(content.Input) > 0 {
				var input BashToolInput
				if json.Unmarshal(content.Input, &input) == nil {
					if input.DangerouslyDisableSandbox {
						return true
					}
				}
			}
		}
	}
	return false
}

// extractContextUsage extracts context usage from the last assistant entry with usage data.
// Returns the percentage of context window used, total input tokens, and the model id.
// Only considers entries after the most recent compact/microcompact boundary,
// since context is reset during compaction.
func extractContextUsage(entries []LogEntry) (float64, int, string) {
	// Find the most recent compact/microcompact boundary
	lastBoundaryIdx := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "system" &&
			(entries[i].Subtype == "compact_boundary" || entries[i].Subtype == "microcompact_boundary") {
			lastBoundaryIdx = i
			break
		}
	}

	// Only look for usage data AFTER the last boundary
	for i := len(entries) - 1; i >= 0; i-- {
		if i <= lastBoundaryIdx {
			break // Don't use pre-compact data
		}

		entry := entries[i]
		if entry.Type != "assistant" || entry.Message == nil || entry.Message.Usage == nil {
			continue
		}

		usage := entry.Message.Usage
		totalTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens + usage.OutputTokens
		if totalTokens == 0 {
			continue
		}

		window := contextWindowForModel(entry.Message.Model)
		percent := float64(totalTokens) / float64(window) * 100
		return percent, totalTokens, entry.Message.Model
	}

	return 0, 0, ""
}

// decodeProjectName converts the directory name to a readable project name
func decodeProjectName(name string) string {
	// Format: -Users-username-Projects-org-project
	// Or:     -Users-username-some-folder
	// We want to extract the meaningful project path

	// Remove leading dash
	name = strings.TrimPrefix(name, "-")

	// Look for common markers to find the project path
	// Try to find "Projects-" marker first
	if idx := strings.Index(name, "-Projects-"); idx != -1 {
		// Everything after "Projects-" is the project path
		projectPath := name[idx+len("-Projects-"):]
		return formatProjectPath(projectPath)
	}

	// If no Projects marker, try to skip Users-username-
	parts := strings.SplitN(name, "-", 3)
	if len(parts) >= 3 && parts[0] == "Users" {
		// Skip "Users-username-" and use the rest
		return formatProjectPath(parts[2])
	}

	// Fallback: return as-is with dashes replaced by slashes
	return strings.ReplaceAll(name, "-", "/")
}

// formatProjectPath formats a project path, converting first dash to slash
// to get "org/project-name" format
func formatProjectPath(path string) string {
	// Split on first dash only to get "org/rest-of-name"
	parts := strings.SplitN(path, "-", 2)
	if len(parts) == 2 {
		return parts[0] + "/" + parts[1]
	}
	return path
}

// DefaultContextWindow is the fallback context window size for Claude models (200K tokens)
const DefaultContextWindow = 200000

// ExtendedContextWindow is the 1M context window available on Opus/Sonnet from
// generation 4.6 onward.
const ExtendedContextWindow = 1_000_000

// ContextWindowForModel is the exported variant of contextWindowForModel,
// for use by the UI layer to label the active context window.
func ContextWindowForModel(model string) int {
	return contextWindowForModel(model)
}

// contextWindowForModel returns the context window size for a given model ID.
// Opus and Sonnet from generation 4.6 onward, plus the Claude 5 family
// (Fable/Sonnet 5), have 1M context windows; Haiku and older models use the
// 200K default.
func contextWindowForModel(model string) int {
	family, major, minor, ok := parseClaudeModel(model)
	if !ok {
		return DefaultContextWindow
	}
	if family != "opus" && family != "sonnet" && family != "fable" {
		return DefaultContextWindow
	}
	if major > 4 || (major == 4 && minor >= 6) {
		return ExtendedContextWindow
	}
	return DefaultContextWindow
}

// parseClaudeModel extracts the family ("opus", "sonnet", "fable", "haiku")
// and the generation (major, minor) from model ids of the form
// "claude-<family>-<major>[-<minor>][-suffix]". The Claude 5 family drops the
// minor version ("claude-fable-5", "claude-sonnet-5"); it is treated as 0.
// Returns ok=false for anything that doesn't match — including "<synthetic>",
// empty strings, and non-numeric majors — so callers can fall back to a safe
// default.
func parseClaudeModel(model string) (family string, major, minor int, ok bool) {
	const prefix = "claude-"
	if !strings.HasPrefix(model, prefix) {
		return "", 0, 0, false
	}
	parts := strings.Split(model[len(prefix):], "-")
	if len(parts) < 2 {
		return "", 0, 0, false
	}
	family = parts[0]
	maj, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, 0, false
	}
	min := 0
	if len(parts) >= 3 {
		if v, err := strconv.Atoi(parts[2]); err == nil {
			min = v
		}
	}
	return family, maj, min, true
}

// GhostThreshold is how long a running process's log must be silent before
// --kill-ghosts will offer to terminate it.
//
// An hour is deliberately far beyond any normal pause. The cost of the two
// mistakes is not symmetric: waiting longer to reap an orphan costs some idle
// memory, while reaping too early kills a session someone is using. A model
// can sit on a single long tool call, and a user can leave a session open over
// lunch, without either being abandoned.
const GhostThreshold = time.Hour

// recentActivityWindow bounds every "Working" inference in determineStatus: a
// tool result, user prompt, assistant message, or progress heartbeat only counts
// as active work while it is younger than this. Older signals age out to Waiting,
// which is what keeps a session from staying stuck on "Working" after Claude has
// yielded back to the user without writing a turn-completion marker.
const recentActivityWindow = 2 * time.Minute

// determineStatus analyzes log entries to determine session status.
// fileModTime is the log file's modification time, used to detect recent writes
// that may not yet appear as parsed entries (e.g., during streaming).
// Returns: status, task description, and whether this is a ghost process.
func determineStatus(entries []LogEntry, isRunning bool, fileModTime time.Time) (Status, string, bool) {
	if len(entries) == 0 {
		if isRunning {
			// Process running but no log entries - new session starting up
			return StatusWaiting, "-", false
		}
		return StatusInactive, "-", false
	}

	var lastAssistant *LogEntry
	var lastUser *LogEntry
	var lastSystem *LogEntry
	var lastProgress *LogEntry
	var lastTimestamp time.Time

	// Find the last relevant entries
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]

		if !entry.Timestamp.IsZero() && entry.Timestamp.After(lastTimestamp) {
			lastTimestamp = entry.Timestamp
		}

		switch entry.Type {
		case "assistant":
			if lastAssistant == nil {
				lastAssistant = &entries[i]
			}
		case "user":
			if lastUser == nil {
				lastUser = &entries[i]
			}
		case "system":
			if lastSystem == nil && entry.Subtype == "turn_duration" {
				lastSystem = &entries[i]
			}
		case "progress", "hook_progress", "agent_progress":
			if lastProgress == nil {
				lastProgress = &entries[i]
			}
		}

		// Stop once we have all we need
		if lastAssistant != nil && lastUser != nil && lastSystem != nil && lastProgress != nil {
			break
		}
	}

	// If Claude is not running, session is inactive
	if !isRunning {
		return StatusInactive, "-", false
	}

	// Check if assistant ended with tool_use (needs approval) - BEFORE ghost check
	// A session waiting for user input is NOT a ghost, even if stale
	hasPendingToolUse := false
	pendingToolName := ""
	if lastAssistant != nil && lastAssistant.Message != nil {
		// Count all tool_use blocks in the assistant message
		toolUseCount := 0
		lastToolName := ""
		for _, content := range lastAssistant.Message.Content {
			if content.Type == "tool_use" {
				toolUseCount++
				lastToolName = content.Name
			}
		}

		if toolUseCount > 0 {
			// Count tool_result blocks in the subsequent user message
			toolResultCount := 0
			if lastUser != nil && lastUser.Timestamp.After(lastAssistant.Timestamp) && lastUser.Message != nil {
				for _, uc := range lastUser.Message.Content {
					if uc.Type == "tool_result" {
						toolResultCount++
					}
				}
			}

			if toolResultCount >= toolUseCount {
				// All tools got results - check if turn completed or still working
				if lastSystem != nil && lastSystem.Timestamp.After(lastUser.Timestamp) {
					// Turn completed after tool results
				} else if time.Since(lastUser.Timestamp) < recentActivityWindow {
					// No turn_duration marker yet, but the tool result is recent —
					// Claude is very likely still working (about to continue the turn).
					return StatusWorking, "Processing...", false
				}
				// All tools resolved but the last result is stale and no
				// turn_duration/end_turn followed. Claude commonly ends a turn here
				// (e.g. asking the user a question) without writing a completion
				// marker, so fall through to the time-based checks below, which
				// resolve this to Waiting/Needs Input rather than a stuck "Working".
			} else {
				// Some tool_use blocks have no result yet
				hasPendingToolUse = true
				pendingToolName = lastToolName
			}
		}
	}

	// If there's a pending tool_use, check recency to decide status.
	// Many tools (Task, Read, Grep, Write, Edit, etc.) are auto-approved and
	// execute without user interaction. A recent pending tool_use likely means
	// the tool is currently executing, not waiting for approval.
	if hasPendingToolUse {
		if lastAssistant != nil && time.Since(lastAssistant.Timestamp) < recentActivityWindow {
			return StatusWorking, "Using: " + pendingToolName, false
		}
		return StatusNeedsInput, "Using: " + pendingToolName, false
	}

	// Check if turn completed (system message with turn_duration).
	// This MUST come before file mod time and progress checks, because the
	// turn_duration write itself updates the file mod time, which would
	// otherwise cause a false "Working" status on a completed turn.
	if lastSystem != nil {
		if lastAssistant == nil || lastSystem.Timestamp.After(lastAssistant.Timestamp) {
			// If a new user message arrived after the turn completed, Claude is
			// working on it — but only while that prompt is recent. A prompt left
			// with no response for minutes (user walked away, or Claude stalled)
			// must not stay pinned on "Working"; fall through to the staleness
			// checks below, which resolve it to Waiting.
			if lastUser != nil && lastUser.Timestamp.After(lastSystem.Timestamp) &&
				time.Since(lastUser.Timestamp) < recentActivityWindow {
				return StatusWorking, "Processing...", false
			}
			if lastUser == nil || !lastUser.Timestamp.After(lastSystem.Timestamp) {
				return StatusWaiting, "-", false
			}
		}
	}

	// If the last assistant message has stop_reason "end_turn", the turn is
	// complete even if no turn_duration system entry has been written yet.
	if lastAssistant != nil && lastAssistant.Message != nil &&
		lastAssistant.Message.StopReason == "end_turn" {
		// Only if no newer user message (which would mean a new turn started)
		if lastUser == nil || !lastUser.Timestamp.After(lastAssistant.Timestamp) {
			return StatusWaiting, "-", false
		}
	}

	// Progress heartbeats (progress, hook_progress, agent_progress) indicate
	// active work: tool execution, hook callbacks, or subagent activity.
	// A recent heartbeat is a strong signal that the session is working.
	if lastProgress != nil && time.Since(lastProgress.Timestamp) < recentActivityWindow {
		task := extractTask(lastAssistant)
		return StatusWorking, task, false
	}

	// If the log file was recently modified (within 30s), the session is actively
	// writing — even if parsed entries are stale (e.g., streaming writes in progress).
	if !fileModTime.IsZero() && time.Since(fileModTime) < 30*time.Second {
		task := extractTask(lastAssistant)
		return StatusWorking, task, false
	}

	// If process is running but log is stale, it's Waiting (not ghost)
	// The user may be away or thinking - this is a valid active session
	// Ghost detection is only for --kill-ghosts to find truly orphaned processes
	if time.Since(lastTimestamp) > 5*time.Minute {
		return StatusWaiting, "-", false
	}

	// If assistant is recent, it's working. Use 2-minute window to avoid
	// flipping to "Waiting" during brief gaps between log writes.
	if lastAssistant != nil {
		task := extractTask(lastAssistant)
		if time.Since(lastAssistant.Timestamp) < recentActivityWindow {
			return StatusWorking, task, false
		}
	}

	// If a genuine user prompt is the most recent entry (e.g. first message in
	// session), Claude is processing it — but only while the prompt is recent. A
	// user message that only carries a tool_result does NOT count: that is the
	// tail of Claude's own turn, not a new prompt, and treating it as work is
	// what made sessions stick on "Working" after Claude yielded back to the user
	// without a turn_duration. The recency bound matters just as much: a genuine
	// prompt left unanswered (user walked away, or Claude stalled) must age out to
	// Waiting instead of staying pinned on "Working".
	if lastUser != nil && (lastAssistant == nil || lastUser.Timestamp.After(lastAssistant.Timestamp)) {
		if isUserPrompt(lastUser) && time.Since(lastUser.Timestamp) < recentActivityWindow {
			return StatusWorking, "Processing...", false
		}
	}

	return StatusWaiting, "-", false
}

// isUserPrompt reports whether a user log entry is a genuine user prompt
// (carries text) rather than only a tool_result echoed back to Claude. Claude's
// tool results are recorded as user-role messages, so distinguishing them is
// what tells a session that yielded to the user apart from one actively working.
func isUserPrompt(entry *LogEntry) bool {
	if entry == nil || entry.Message == nil {
		return false
	}
	for _, content := range entry.Message.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			return true
		}
	}
	return false
}

// extractTask extracts a task description from an assistant entry
func extractTask(entry *LogEntry) string {
	if entry == nil || entry.Message == nil {
		return "-"
	}

	for _, content := range entry.Message.Content {
		if content.Type == "tool_use" && content.Name != "" {
			return "Using: " + content.Name
		}
		if content.Type == "text" && content.Text != "" {
			// Truncate long text
			text := content.Text
			if len(text) > 50 {
				text = text[:47] + "..."
			}
			// Take first line only
			if idx := strings.Index(text, "\n"); idx > 0 {
				text = text[:idx]
			}
			return text
		}
	}

	return "-"
}

// GhostProcess represents an orphaned Claude process
type GhostProcess struct {
	PID     int
	Project string
	Age     time.Duration
}

// FindGhostProcesses returns Claude processes that are running but whose log
// has been silent for longer than GhostThreshold.
func FindGhostProcesses() ([]GhostProcess, error) {
	sessions, err := Discover()
	if err != nil {
		return nil, err
	}
	return ghostsFrom(sessions), nil
}

// ghostsFrom selects the stale sessions whose pid is certainly their own.
//
// Staleness is a property of the log, but the pid is paired to that log by
// position: logs arrive sorted newest-first while pids arrive in ps order, and
// the two orderings have no relationship. In a directory running one Claude the
// pairing is the only one available and therefore correct; with several, the
// stale log can carry the busy process's pid. Since the caller sends SIGTERM to
// whatever this returns, an unconfident pairing is not "some pid for this
// directory" -- it is a coin flip over which session dies.
func ghostsFrom(sessions []Session) []GhostProcess {
	var ghosts []GhostProcess
	seenPIDs := make(map[int]bool)
	for _, s := range sessions {
		if s.GhostPID == 0 {
			continue
		}
		if !s.PIDConfident {
			continue
		}
		// Several sessions in one project can resolve to the same process.
		if seenPIDs[s.GhostPID] {
			continue
		}
		seenPIDs[s.GhostPID] = true

		age := time.Since(s.LastActivity)
		if age > GhostThreshold {
			ghosts = append(ghosts, GhostProcess{
				PID:     s.GhostPID,
				Project: s.Project,
				Age:     age,
			})
		}
	}
	return ghosts
}

// isClaudeProcess checks whether the given PID belongs to a process named "claude".
// This guards against PID reuse where a stale PID now belongs to an unrelated process.
func isClaudeProcess(pid int) bool {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	comm := strings.TrimSpace(string(out))
	return strings.HasSuffix(comm, "claude")
}

// KillGhostProcesses sends SIGTERM to every ghost process.
//
// It returns the processes it terminated and, separately, the ones it could
// not. A process that refuses the signal (it belongs to another user, or it is
// protected) is not the same as one that had already exited, and a command
// whose whole job is killing things should not report a shortfall it declines
// to explain.
func KillGhostProcesses() (killed []GhostProcess, failed []GhostKillFailure, err error) {
	ghosts, err := FindGhostProcesses()
	if err != nil {
		return nil, nil, err
	}

	for _, ghost := range ghosts {
		// The pid may have been recycled by an unrelated process since Discover.
		if !isClaudeProcess(ghost.PID) {
			continue
		}

		process, findErr := os.FindProcess(ghost.PID)
		if findErr != nil {
			failed = append(failed, GhostKillFailure{Ghost: ghost, Err: findErr})
			continue
		}

		if sigErr := process.Signal(syscall.SIGTERM); sigErr != nil {
			// ESRCH means it exited on its own between listing and signalling,
			// which is the one failure that needs no explanation.
			if !errors.Is(sigErr, os.ErrProcessDone) && !errors.Is(sigErr, syscall.ESRCH) {
				failed = append(failed, GhostKillFailure{Ghost: ghost, Err: sigErr})
			}
			continue
		}

		killed = append(killed, ghost)
	}

	return killed, failed, nil
}

// GhostKillFailure is a ghost process that would not accept SIGTERM, paired
// with the reason.
type GhostKillFailure struct {
	Ghost GhostProcess
	Err   error
}

// FormatAge formats a duration as a human-readable age string
func FormatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
