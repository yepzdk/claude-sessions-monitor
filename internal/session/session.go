// Package session discovers Claude Code sessions and decides what state each
// one is in. It joins two sources — the JSONL logs under ~/.claude/projects and
// a scan of running claude processes — into []Session, which the ui, web and
// jump packages consume without touching either source themselves. It also
// holds history, timelines, token usage, API quota and origin detection.
//
// docs/ARCHITECTURE.md walks through the data flow, the status rules, ghost
// semantics and the test conventions for this package.
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

// Session represents a session of one of the coding agents csm watches
type Session struct {
	Project      string    `json:"project"`
	Status       Status    `json:"status"`
	LastActivity time.Time `json:"last_activity"`
	Task         string    `json:"task"`
	Summary      string    `json:"summary,omitempty"`
	LastMessage  string    `json:"last_message,omitempty"`
	LogFile      string    `json:"log_file"`
	ProjectPath  string    `json:"-"`                    // Encoded project directory name (as used under ~/.claude/projects)
	CWD          string    `json:"-"`                    // Absolute working directory recorded in the log
	SessionID    string    `json:"session_id,omitempty"` // Session UUID (log filename stem)
	Harness      Harness   `json:"harness"`              // Which coding agent this session belongs to
	Origin       Origin    `json:"origin,omitempty"`     // Where the session was launched from
	IsGhost      bool      `json:"is_ghost,omitempty"`   // True if process running but log is stale
	GhostPID     int       `json:"ghost_pid,omitempty"`  // PID of the ghost process (for killing)
	PIDConfident bool      `json:"pid_confident"`        // True when GhostPID is certainly this session's process, not a positional guess
	// ContextWindow is the model's context size in tokens. The dashboard needs
	// the decision, not the model id: reimplementing the id parsing in
	// JavaScript let the two disagree about the same session.
	ContextWindow int `json:"context_window,omitempty"`
	// Degraded names the reason this session's log could not be read in full.
	// Empty means the data below is complete. Anything else means some of it
	// is missing, and the UI marks the row so the numbers are not read as
	// measurements.
	Degraded       string     `json:"degraded,omitempty"`
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
		// A content shape this parser does not know costs one message's text,
		// not the session: returning the error would abort the whole log and
		// blank a dashboard row over a field nothing here reads.
		return nil //nolint:nilerr
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

// listProcesses shells out to ps. It is a variable so a test can make the
// process scan fail, which is the case that used to be indistinguishable from
// "nothing is running".
//
// args= rather than comm=, because a coding agent is not always its own
// argv[0]: omp runs as `bun /path/to/omp`, so comm is `bun` and the only
// evidence of what the process is sits in the arguments. tty= rides along
// because it costs nothing here and is how an omp session's log is matched to
// the process running it.
var listProcesses = func() ([]byte, error) {
	// ps directly, with no shell pipeline, to avoid shell injection risks.
	return exec.Command("ps", "ax", "-o", "pid=,ppid=,tty=,args=").Output()
}

// psLine is one parsed row of `ps -o pid=,ppid=,tty=,args=`.
type psLine struct {
	pid, ppid int
	tty       string // controlling terminal, or ps's placeholder when there is none
	argv      string // full command line, whitespace-normalised
}

// parsePSOutput splits ps output into rows, skipping anything malformed.
func parsePSOutput(output []byte) []psLine {
	var rows []psLine
	for _, line := range bytes.Split(output, []byte("\n")) {
		fields := bytes.Fields(line)
		// pid, ppid, tty, and at least one argv token. A row with no command
		// line cannot be attributed to a harness, so dropping it here loses
		// nothing.
		if len(fields) < 4 {
			continue
		}
		// Atoi rather than Sscanf: a malformed field is reported instead of
		// leaving the variable at zero and relying on the check below.
		pid, err := strconv.Atoi(string(fields[0]))
		if err != nil || pid == 0 {
			continue
		}
		ppid, err := strconv.Atoi(string(fields[1]))
		if err != nil {
			continue
		}
		rows = append(rows, psLine{
			pid:  pid,
			ppid: ppid,
			tty:  string(fields[2]),
			// One allocation. This runs over every process on the machine,
			// twice a second.
			argv: string(bytes.Join(fields[3:], []byte(" "))),
		})
	}
	return rows
}

// getRunningHarnessProcs returns one entry per running coding-agent process,
// carrying the harness it belongs to, its working directory, its controlling
// terminal and whether it has been orphaned.
//
// It reports the error rather than an empty slice: "nothing is running" and
// "the process scan failed" produce identical results downstream, and every
// session would be reported Inactive and filtered out of the dashboard. csm
// would say "No active sessions." with total confidence while sessions ran.
//
// A process whose cwd cannot be read is dropped, because a project is the only
// thing callers use the cwd for and an unplaceable process cannot be joined to
// any log directory.
//
// orphan (ppid 1) is a field on the process rather than a side lookup because
// it is read off this same ps output; splitting the two let them disagree about
// what was running. That, not silence, is what distinguishes a ghost from a
// session someone left open overnight.
//
// ponytail: ppid==1 is exact on macOS; on Linux a subreaper (some systemd
// user sessions) can adopt orphans instead of pid 1, which this misses.
func getRunningHarnessProcs() ([]harnessProcess, error) {
	output, err := listProcesses()
	if err != nil {
		return nil, fmt.Errorf("listing processes with ps: %w", err)
	}

	var procs []harnessProcess
	for _, row := range parsePSOutput(output) {
		harness := classifyProcess(row.argv)
		if harness == HarnessNone {
			continue
		}
		cwd, err := getProcessCwd(row.pid)
		if err != nil || cwd == "" {
			continue
		}
		procs = append(procs, harnessProcess{
			pid:     row.pid,
			harness: harness,
			cwd:     cwd,
			tty:     row.tty,
			orphan:  row.ppid == 1,
		})
	}

	return procs, nil
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

// Discover finds every live and recently-live session, from both agents.
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

	// A missing projects directory means Claude Code has never run here, which
	// is an ordinary state now that csm watches a second agent: aborting on it
	// meant an omp-only machine got "Cannot read sessions: no such file or
	// directory" on every tick and never reached discoverOMP below. Any other
	// read error still aborts — that one is a real fault, and reporting no
	// sessions would be indistinguishable from having none.
	entries, err := os.ReadDir(projectsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Get every running coding-agent process, along with Claude Code's own
	// pid <-> session registry when this version writes one. Both come from one
	// TTL-cached snapshot so they cannot disagree about what is running, and so
	// ps/lsof are not spawned on every refresh.
	procs, registry, haveRegistry, err := cachedRunningHarnessProcs()
	if err != nil {
		return nil, err
	}
	// Claude Code buckets its logs by the encoded working directory, so keying
	// the processes the same way makes the join a plain string-key match.
	runningDirs := pidsByDir(procs, HarnessClaude, encodeProjectPath)
	procByPID := procsByPID(procs)

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
		if haveRegistry {
			// A live session that has been quiet longer than the freshness
			// window loses its positional slot to any fresher log, including
			// one from a session that already exited. The registry knows it
			// is alive, so list it regardless.
			present := make(map[string]bool, len(logFiles))
			for _, f := range logFiles {
				present[f] = true
			}
			for _, f := range registryLogsForDir(registry, projectDir) {
				if !present[f] {
					logFiles = append(logFiles, f)
				}
			}
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
			// With a registry the pairing is exact and per-session; without
			// one it is the positional guess described above, and only
			// identifies a process when there's exactly one candidate on
			// each side. Anything that needs to be *right* about which
			// process belongs to this session (rather than merely "some pid
			// for this directory", which is all --kill-ghosts needs) must
			// check PIDConfident first.
			isRunning, pid, pidConfident := pairProcess(
				sessionIDFromLogFile(logFile), entry.Name(), registry, haveRegistry, pids, i, len(logFiles))
			// Orphan-ness follows whichever pid the pairing settled on; with
			// no pid (unconfident or unknown process) the session cannot be
			// a ghost, which is the safe side.
			orphaned := procByPID[pid].orphan

			session, err := parseSession(entry.Name(), logFile, isRunning, pid, orphaned)
			if err != nil {
				continue
			}
			session.PIDConfident = pidConfident && session.GhostPID > 0

			sessions = append(sessions, session)
		}
	}

	// The second producer. It reads a different store in a different format and
	// returns the same []Session, which is why every view downstream needs no
	// idea that more than one agent exists.
	sessions = append(sessions, discoverOMP(procs, liveFiles)...)

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

// logCandidate is one session log a directory holds, with the stat fields the
// selection and the parse cache both need.
type logCandidate struct {
	path    string
	modTime time.Time
	size    int64
}

// listLogsByRecency returns a project directory's session logs, newest first.
//
// Split out from findActiveLogs so a caller that needs to look at the newest log
// before it knows how many processes are running -- discoverOMP, which reads the
// bucket's working directory out of that log -- does not have to scan the
// directory twice per tick. It also hands back modTime and size, so that caller
// can go straight to the parse cache instead of stat-ing again.
func listLogsByRecency(dir string) ([]logCandidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var logs []logCandidate
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
		logs = append(logs, logCandidate{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
			size:    info.Size(),
		})
	}

	sort.Slice(logs, func(i, j int) bool {
		return logs[i].modTime.After(logs[j].modTime)
	})
	return logs, nil
}

// selectActiveLogs picks which of a directory's logs are worth parsing.
// If runningCount > 0, it returns at least that many (the most recently
// modified), plus any additional file modified within activeLogFreshnessWindow.
// If runningCount == 0, it returns only the single most recent file.
func selectActiveLogs(logs []logCandidate, runningCount int) []string {
	if len(logs) == 0 {
		return nil
	}

	if runningCount == 0 {
		// No running processes: return only the most recent file
		// Prefer newest non-empty, but return empty if it's newer (fresh session)
		for _, l := range logs {
			if l.size > 0 {
				// Check if there's an even newer empty file
				if logs[0].size == 0 && logs[0].modTime.After(l.modTime) {
					return []string{logs[0].path}
				}
				return []string{l.path}
			}
		}
		// All empty, return newest
		return []string{logs[0].path}
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

	return result
}

// findActiveLogs returns all active JSONL log files for a project directory.
func findActiveLogs(dir string, runningCount int) ([]string, error) {
	logs, err := listLogsByRecency(dir)
	if err != nil {
		return nil, err
	}
	return selectActiveLogs(logs, runningCount), nil
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

// newLogScanner returns a line scanner for a JSONL session log, bounded at
// maxLineBytes.
//
// The initial buffer's capacity must not exceed maxLineBytes: bufio.Scanner only
// grows a token buffer when it needs to, so a capacity already bigger than
// maxLineBytes would let a token past that limit through untouched. Both
// harnesses' parsers want exactly this, and a second copy of the reasoning is a
// second place for it to rot.
func newLogScanner(file *os.File, maxLineBytes int) *bufio.Scanner {
	scanner := bufio.NewScanner(file)
	initialBufSize := 64 * 1024
	if maxLineBytes < initialBufSize {
		initialBufSize = maxLineBytes
	}
	scanner.Buffer(make([]byte, 0, initialBufSize), maxLineBytes)
	return scanner
}

// parseLogFileWithLimit is parseLogFile with the scanner's max-line-size made
// an explicit parameter, so tests can reproduce an oversized-line scan error
// without allocating a real maxLogLineBytes-sized line.
func parseLogFileWithLimit(logFile string, keep int, maxLineBytes int) (parsedLog, error) {
	file, err := os.Open(logFile)
	if err != nil {
		return parsedLog{}, err
	}
	defer func() { _ = file.Close() }()

	var pl parsedLog
	var entries []LogEntry

	scanner := newLogScanner(file, maxLineBytes)

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

// parseSession parses a Claude Code session from its log file
func parseSession(projectName, logFile string, isRunning bool, pid int, orphaned bool) (Session, error) {
	session := Session{
		Project:     decodeProjectName(projectName),
		LogFile:     logFile,
		Status:      StatusInactive, // Default to inactive
		ProjectPath: projectName,    // Store the encoded name for matching
		SessionID:   sessionIDFromLogFile(logFile),
		Harness:     HarnessClaude,
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
	// A read failure here is not the same as an idle session. isRunning and pid
	// are already known and correct, and determineStatus turns "running process,
	// no readable entries" into Waiting. Returning early skipped that and left
	// the Inactive default, which ActiveSessions then filtered out entirely --
	// so a session that was working, possibly sitting on an approval prompt,
	// vanished from the dashboard and from the counts.
	pl, err := cachedParseLogFile(logFile, info.ModTime(), info.Size(), 100)
	if err != nil {
		session.Degraded = err.Error()
	}

	applyParsedLog(&session, pl, isRunning, pid, orphaned, info.ModTime())
	return session, nil
}

// applyParsedLog populates a Session from a parsedLog. The file-derived fields
// come straight from pl (cacheable); the status and PID fields are recomputed
// on every call because they depend on wall-clock time and the running-process
// set, both of which change without the file changing.
func applyParsedLog(session *Session, pl parsedLog, isRunning bool, pid int, orphaned bool, fileModTime time.Time) {
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
	if pl.model != "" {
		session.ContextWindow = contextWindowForModel(pl.model)
	}

	// Time-relative + running-dependent: must be recomputed each call.
	session.Status, session.Task = determineStatus(pl.entries, isRunning, fileModTime)

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

	// Derived last, once LastActivity has settled: a ghost is a live process
	// nobody can reach anymore -- its parent is gone -- whose log has also
	// stopped moving. Silence alone is not enough: a tab left open overnight
	// is silent for hours and is not a ghost. determineStatus cannot decide
	// this because it runs before lastEntryTime is applied.
	session.IsGhost = isRunning && orphaned && time.Since(session.LastActivity) > GhostThreshold
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
	if len(parts) >= 3 {
		if v, err := strconv.Atoi(parts[2]); err == nil {
			minor = v
		}
	}
	return family, maj, minor, true
}

// GhostThreshold is how long an orphaned process's log must be silent before
// it is reported as a ghost and --kill-ghosts will offer to terminate it.
//
// Being orphaned (harnessProcess.orphan, set by getRunningHarnessProcs) is the
// primary signal; the hour of silence guards the one legitimate orphan, a
// headless `claude -p` job whose
// launching shell has exited but which is still producing output. Waiting
// longer to reap a true orphan costs some idle memory; reaping too early kills
// work in progress.
const GhostThreshold = time.Hour

// recentActivityWindow bounds every "Working" inference in determineStatus: a
// tool result, user prompt, assistant message, or progress heartbeat only counts
// as active work while it is younger than this. Older signals age out to Waiting,
// which is what keeps a session from staying stuck on "Working" after Claude has
// yielded back to the user without writing a turn-completion marker.
const recentActivityWindow = 2 * time.Minute

// logWriteWindow is how recently the log file must have been written for the
// write itself to count as evidence of work, independent of what the parsed
// entries say. It covers a session mid-write, whose newest entry is not on disk
// yet.
const logWriteWindow = 30 * time.Second

// sessionStaleWindow is the age past which a running session's newest entry
// stops supporting any "Working" inference at all. Both harnesses use these
// three windows, so that the status column means the same thing on every row of
// a mixed dashboard.
const sessionStaleWindow = 5 * time.Minute

// determineStatus analyzes log entries to determine session status.
// fileModTime is the log file's modification time, used to detect recent writes
// that may not yet appear as parsed entries (e.g., during streaming).
// Returns the status and a short task description.
func determineStatus(entries []LogEntry, isRunning bool, fileModTime time.Time) (Status, string) {
	if len(entries) == 0 {
		if isRunning {
			// Process running but no log entries - new session starting up
			return StatusWaiting, "-"
		}
		return StatusInactive, "-"
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
		return StatusInactive, "-"
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
					return StatusWorking, "Processing..."
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
			return StatusWorking, "Using: " + pendingToolName
		}
		return StatusNeedsInput, "Using: " + pendingToolName
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
				return StatusWorking, "Processing..."
			}
			if lastUser == nil || !lastUser.Timestamp.After(lastSystem.Timestamp) {
				return StatusWaiting, "-"
			}
		}
	}

	// If the last assistant message has stop_reason "end_turn", the turn is
	// complete even if no turn_duration system entry has been written yet.
	if lastAssistant != nil && lastAssistant.Message != nil &&
		lastAssistant.Message.StopReason == "end_turn" {
		// Only if no newer user message (which would mean a new turn started)
		if lastUser == nil || !lastUser.Timestamp.After(lastAssistant.Timestamp) {
			return StatusWaiting, "-"
		}
	}

	// Progress heartbeats (progress, hook_progress, agent_progress) indicate
	// active work: tool execution, hook callbacks, or subagent activity.
	// A recent heartbeat is a strong signal that the session is working.
	if lastProgress != nil && time.Since(lastProgress.Timestamp) < recentActivityWindow {
		task := extractTask(lastAssistant)
		return StatusWorking, task
	}

	// A user message that carries only a tool_result does NOT count as a prompt:
	// it is the tail of Claude's own turn, and treating it as work is what made
	// sessions stick on "Working" after Claude yielded back to the user without
	// a turn_duration. omp needs no such test, which is why the decision is
	// made here rather than inside the shared tail.
	var lastPrompt time.Time
	if lastUser != nil && (lastAssistant == nil || lastUser.Timestamp.After(lastAssistant.Timestamp)) &&
		isUserPrompt(lastUser) {
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
		task:          func() string { return extractTask(lastAssistant) },
	})
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

// taskLabel condenses a message into the single line the task column shows.
//
// Runes, not bytes. The byte slice this replaces (`text[:47]`) puts a
// replacement character in the dashboard the first time a task begins with
// anything non-ASCII. The line cut comes first so that a long first line is
// capped, rather than a cap landing past a newline and hiding it.
func taskLabel(text string) string {
	if text == "" {
		return "-"
	}
	if idx := strings.Index(text, "\n"); idx > 0 {
		text = text[:idx]
	}
	if runes := []rune(text); len(runes) > 50 {
		return string(runes[:47]) + "..."
	}
	return text
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
			return taskLabel(content.Text)
		}
	}

	return "-"
}

// activity is what the shared tail of both harnesses' status rules needs: four
// moments and a way to label the work. Timestamps are zero when the thing they
// describe does not exist.
type activity struct {
	fileModTime time.Time
	// lastEntry is the newest timestamp of any entry, however uninteresting.
	lastEntry time.Time
	// lastAssistant is when the agent last said something.
	lastAssistant time.Time
	// lastPrompt is when the user last said something *that counts as a prompt
	// and is newer than lastAssistant*. Deciding that is the harness's job:
	// Claude Code records tool results as user messages and omp does not.
	lastPrompt time.Time
	// task labels a Working row. Called only on the paths that report Working,
	// so neither caller builds a label it will not use.
	task func() string
}

// ageStatus is the tail both determineStatus and determineOMPStatus end in.
//
// Once each harness's own evidence has had its say -- pending tools, turn
// markers, exits -- what is left is the same four rules about how recently
// anything happened, on the same three windows. Sharing them is what makes
// "both harnesses must mean the same thing by Working" structural instead of a
// comment two functions are asked to honour.
func ageStatus(a activity) (Status, string) {
	// The write itself is evidence, for the moments when the newest entry is not
	// on disk yet.
	if !a.fileModTime.IsZero() && time.Since(a.fileModTime) < logWriteWindow {
		return StatusWorking, a.task()
	}

	// Running, but nothing has happened for long enough that no signal below
	// deserves to be read as work in progress.
	if time.Since(a.lastEntry) > sessionStaleWindow {
		return StatusWaiting, "-"
	}

	if !a.lastAssistant.IsZero() && time.Since(a.lastAssistant) < recentActivityWindow {
		return StatusWorking, a.task()
	}

	// A prompt with no answer yet. The recency bound matters as much as the
	// prompt: one left unanswered because the user walked away must age out to
	// Waiting rather than stay pinned on Working.
	if !a.lastPrompt.IsZero() && time.Since(a.lastPrompt) < recentActivityWindow {
		return StatusWorking, "Processing..."
	}

	return StatusWaiting, "-"
}

// GhostProcess represents an orphaned coding agent process
type GhostProcess struct {
	PID     int
	Project string
	Age     time.Duration
	// Harness is the agent this process is believed to belong to. It is what
	// the pre-SIGTERM recheck is verified against, so it must come from the
	// session, not from a fresh guess about the pid.
	Harness Harness
}

// FindGhostProcesses returns Claude processes that have lost their parent and
// whose log has been silent for longer than GhostThreshold.
func FindGhostProcesses() ([]GhostProcess, error) {
	sessions, err := Discover()
	if err != nil {
		return nil, err
	}
	return ghostsFrom(sessions), nil
}

// ghostsFrom selects the ghost sessions whose pid is certainly their own.
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

		if s.IsGhost {
			ghosts = append(ghosts, GhostProcess{
				PID:     s.GhostPID,
				Project: s.Project,
				Age:     time.Since(s.LastActivity),
				Harness: s.Harness,
			})
		}
	}
	return ghosts
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
		// The pid may have been recycled by an unrelated process since
		// Discover, and "unrelated" includes a process belonging to a
		// different harness.
		if !isHarnessProcess(ghost.PID, ghost.Harness) {
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
