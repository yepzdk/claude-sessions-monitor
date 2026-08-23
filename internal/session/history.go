package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HistorySession represents a past Claude session with duration and context
type HistorySession struct {
	Project      string        `json:"project"`
	GitBranch    string        `json:"git_branch"`
	StartTime    time.Time     `json:"start_time"`
	EndTime      time.Time     `json:"end_time"`
	Duration     time.Duration `json:"duration"`
	MessageCount int           `json:"message_count"`
	FirstPrompt  string        `json:"first_prompt"`
	LastMessage  string        `json:"last_message,omitempty"`
	LogFile      string        `json:"log_file"`
}

// SessionIndex represents the structure of sessions-index.json
type SessionIndex struct {
	Version int          `json:"version"`
	Entries []IndexEntry `json:"entries"`
}

// IndexEntry represents a single session entry in the index
type IndexEntry struct {
	SessionID    string `json:"sessionId"`
	FullPath     string `json:"fullPath"`
	Created      string `json:"created"`
	Modified     string `json:"modified"`
	MessageCount int    `json:"messageCount"`
	FirstPrompt  string `json:"firstPrompt"`
	GitBranch    string `json:"gitBranch"`
	ProjectPath  string `json:"projectPath"`
	IsSidechain  bool   `json:"isSidechain"`
}

// DiscoverHistory finds all sessions from the past N days.
// It merges sessions from sessions-index.json files with a direct scan
// of .jsonl files so that projects without an index are also included.
func DiscoverHistory(days int) ([]HistorySession, error) {
	projectsDir, err := ClaudeProjectsDir()
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().AddDate(0, 0, -days)

	// Track seen log files to avoid duplicates
	seen := make(map[string]bool)
	var sessions []HistorySession

	// Phase 1: Collect sessions from sessions-index.json files (richest metadata)
	pattern := filepath.Join(projectsDir, "*", "sessions-index.json")
	indexFiles, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	for _, indexFile := range indexFiles {
		entries, err := parseSessionIndex(indexFile)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			// Skip sidechain sessions
			if entry.IsSidechain {
				continue
			}

			// Parse timestamps (index files use RFC3339 with milliseconds)
			startTime, err := time.Parse(time.RFC3339Nano, entry.Created)
			if err != nil {
				continue
			}

			// Filter by date range
			if startTime.Before(cutoff) {
				continue
			}

			endTime, err := time.Parse(time.RFC3339Nano, entry.Modified)
			if err != nil {
				endTime = startTime
			}

			// Calculate duration
			duration := endTime.Sub(startTime)

			// Extract project name from path
			project := extractProjectName(entry.ProjectPath)

			sessions = append(sessions, HistorySession{
				Project:      project,
				GitBranch:    entry.GitBranch,
				StartTime:    startTime,
				EndTime:      endTime,
				Duration:     duration,
				MessageCount: entry.MessageCount,
				FirstPrompt:  entry.FirstPrompt,
				LogFile:      entry.FullPath,
			})
			seen[entry.FullPath] = true
		}
	}

	// Phase 2: Scan all project directories for .jsonl files not in any index
	projectDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}

	for _, dir := range projectDirs {
		if !dir.IsDir() || strings.HasPrefix(dir.Name(), ".") {
			continue
		}

		projectDir := filepath.Join(projectsDir, dir.Name())
		projectName := decodeProjectName(dir.Name())

		files, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			// Skip agent/sidechain files
			if strings.HasPrefix(f.Name(), "agent-") {
				continue
			}

			logFile := filepath.Join(projectDir, f.Name())
			if seen[logFile] {
				continue
			}

			info, err := f.Info()
			if err != nil || info.Size() == 0 {
				continue
			}

			// Use file modification time for quick cutoff check before parsing
			if info.ModTime().Before(cutoff) {
				continue
			}

			st, statsErr := QuickSessionStats(logFile)
			msgCount, startTime, endTime := st.MessageCount, st.StartTime, st.EndTime
			branch, prompt, sessionCwd := st.GitBranch, st.FirstPrompt, st.CWD
			_ = statsErr // a partial scan still describes the session better than skipping it
			if startTime.IsZero() {
				startTime = info.ModTime()
			}
			if endTime.IsZero() {
				endTime = info.ModTime()
			}

			// Re-check cutoff against actual start time
			if startTime.Before(cutoff) {
				continue
			}

			// Use cwd for accurate project naming when available
			displayName := projectName
			if sessionCwd != "" {
				displayName = extractProjectName(sessionCwd)
			}

			sessions = append(sessions, HistorySession{
				Project:      displayName,
				GitBranch:    branch,
				FirstPrompt:  prompt,
				StartTime:    startTime,
				EndTime:      endTime,
				Duration:     endTime.Sub(startTime),
				MessageCount: msgCount,
				LogFile:      logFile,
			})
			seen[logFile] = true
		}
	}

	// Sort by start time descending (newest first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartTime.After(sessions[j].StartTime)
	})

	return sessions, nil
}

// parseSessionIndex reads and parses a sessions-index.json file
func parseSessionIndex(path string) ([]IndexEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var index SessionIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	return index.Entries, nil
}

// extractProjectName extracts a readable project name from a full path
func extractProjectName(fullPath string) string {
	// Try well-known directory markers (most specific first)
	markers := []string{"/Projects/", "/repos/", "/src/", "/code/", "/workspace/"}
	for _, marker := range markers {
		if idx := strings.Index(fullPath, marker); idx != -1 {
			return fullPath[idx+len(marker):]
		}
	}

	// Detect /home/<user>/X pattern: skip the home directory prefix
	if strings.HasPrefix(fullPath, "/home/") {
		// /home/user/repos/myproject -> repos/myproject
		rest := fullPath[len("/home/"):]
		if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
			afterUser := rest[slashIdx+1:]
			if afterUser != "" {
				return afterUser
			}
		}
	}

	// Fallback: last two path components
	parts := strings.Split(fullPath, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}

	return filepath.Base(fullPath)
}

// SessionStats is what a fast scan of a JSONL log can tell us about a session
// without parsing every line as JSON.
//
// These used to be seven positional return values, four of them adjacent bare
// strings, so two of them could be swapped at a call site or inside the scan
// loop without the compiler noticing.
type SessionStats struct {
	MessageCount int
	StartTime    time.Time
	EndTime      time.Time
	GitBranch    string
	FirstPrompt  string
	CWD          string
	CustomTitle  string
}

// QuickSessionStats scans a JSONL log file for the fields the history view
// needs, without full JSON parsing of every line.
//
// A non-nil error means the scan stopped early and the counts and time range
// below it are incomplete. Callers must not present them as a finished
// reading: a read failure previously surfaced as "0 messages, 0s duration"
// for a session someone had spent an afternoon in.
func QuickSessionStats(logFile string) (stats SessionStats, err error) {
	file, err := os.Open(logFile)
	if err != nil {
		return stats, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Count user prompts only (exclude tool results and assistant messages)
		isUserMsg := strings.Contains(line, `"type":"user"`) && !strings.Contains(line, `"type":"tool_result"`)
		if isUserMsg {
			stats.MessageCount++
			// Capture first user prompt text
			if stats.FirstPrompt == "" {
				stats.FirstPrompt = extractPromptFromLine(line)
			}
		}

		// Extract git branch (keep last non-empty value, branch can change mid-session)
		if b := extractStringField(line, `"stats.GitBranch":"`); b != "" {
			stats.GitBranch = b
		}

		// Extract stats.CWD (use first non-empty value, stays constant within a session)
		if stats.CWD == "" {
			if c := extractStringField(line, `"stats.CWD":"`); c != "" {
				stats.CWD = c
			}
		}

		// Extract custom title (keep last non-empty value, title can change mid-session)
		if t := extractStringField(line, `"stats.CustomTitle":"`); t != "" {
			stats.CustomTitle = t
		}

		// Extract timestamp via string matching (avoids full JSON parse)
		if ts := extractTimestampFromLine(line); !ts.IsZero() {
			if stats.StartTime.IsZero() {
				stats.StartTime = ts
			}
			stats.EndTime = ts
		}
	}

	// bufio.Scanner stops silently on a read error or an oversized line, so
	// without this the partial counts would be returned as a complete reading.
	if scanErr := scanner.Err(); scanErr != nil {
		return stats, fmt.Errorf("scan %s: %w", logFile, scanErr)
	}
	return stats, nil
}

// extractStringField extracts a JSON string value using fast string matching.
// prefix should include the opening quote, e.g. `"fieldName":"`.
func extractStringField(line, prefix string) string {
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(prefix)
	end := strings.IndexByte(line[start:], '"')
	if end <= 0 {
		return ""
	}
	return line[start : start+end]
}

// extractPromptFromLine extracts user prompt text from a JSONL line using
// fast string matching. Handles both plain string content and object arrays.
// Turns that are pure Claude Code scaffolding (a local-command-caveat marker,
// a slash-command invocation) carry no user-authored text, so they're reduced
// to the invoked command or skipped rather than shown as raw wrapper markup.
func extractPromptFromLine(line string) string {
	text := rawPromptText(line)
	if text == "" {
		return ""
	}

	if preview, isScaffolding := commandScaffoldingPreview(text); isScaffolding {
		return preview
	}

	return truncateString(text, 120)
}

// commandScaffoldingPreview reports whether text is a Claude Code scaffolding
// turn -- a slash-command invocation or a local-command caveat block -- and if
// so what to preview for it: the invoked command, or "" to skip the turn.
//
// The markers are recognised only at the start of the turn. Scaffolding Claude
// Code generates always leads with one and carries nothing else; a human prompt
// that merely mentions the markup ("why does <command-name> show up in my
// previews?") does not, and keeps its own text. Note that these turns are not
// reliably flagged isMeta -- on current logs caveat blocks are but slash-command
// invocations are not -- so the markers themselves are what identify them.
func commandScaffoldingPreview(text string) (string, bool) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(trimmed, "<command-") && !strings.HasPrefix(trimmed, "<local-command-") {
		return "", false
	}

	const openTag, closeTag = "<command-name>", "</command-name>"
	if idx := strings.Index(trimmed, openTag); idx >= 0 {
		start := idx + len(openTag)
		if end := strings.Index(trimmed[start:], closeTag); end >= 0 {
			return truncateString(strings.TrimSpace(trimmed[start:start+end]), 120), true
		}
	}
	return "", true
}

// rawPromptText extracts and JSON-unescapes the first user-message content
// found in a JSONL line, trying both plain string content and object array
// content shapes.
func rawPromptText(line string) string {
	// Try plain string content: "content":"..."
	const contentStr = `"content":"`
	if idx := strings.Index(line, contentStr); idx >= 0 {
		start := idx + len(contentStr)
		if text := extractQuotedValue(line, start); text != "" {
			return text
		}
	}

	// Try object array content with text field: "content":[{"type":"text","text":"..."}]
	const contentArr = `"content":[`
	if cidx := strings.Index(line, contentArr); cidx >= 0 {
		const textField = `"text":"`
		if tidx := strings.Index(line[cidx:], textField); tidx >= 0 {
			start := cidx + tidx + len(textField)
			if text := extractQuotedValue(line, start); text != "" {
				return text
			}
		}
	}

	return ""
}

// extractQuotedValue extracts text starting at position until the next
// unescaped double quote.
func extractQuotedValue(line string, start int) string {
	if start >= len(line) {
		return ""
	}
	i := start
	hasEscape := false
	for i < len(line) {
		if line[i] == '\\' {
			hasEscape = true
			i += 2 // skip escaped character
			continue
		}
		if line[i] == '"' {
			break
		}
		i++
	}
	if i <= start || i > len(line) {
		return ""
	}
	raw := line[start:i]
	if !hasEscape {
		return raw
	}
	return unescapeJSONString(raw)
}

// unescapeJSONString decodes JSON string escapes in a raw, still-escaped
// JSON string value by re-quoting it and letting encoding/json decode it.
//
// Best-effort: the raw slice can be malformed on its own -- a half-written
// JSONL line leaves extractQuotedValue no closing quote to stop at, so it
// returns everything to end of line, possibly cut mid-escape. There is nothing
// better to show for those than the escaped text, so that is what comes back.
func unescapeJSONString(s string) string {
	var decoded string
	if err := json.Unmarshal([]byte(`"`+s+`"`), &decoded); err != nil {
		return s
	}
	return decoded
}

// truncateString truncates s to a maximum visible length (in runes, not bytes),
// appending "..." if truncated. Counting runes keeps multi-byte UTF-8 characters
// from being split mid-character, which would leave a replacement character at
// the end of the preview. Mirrors ui.truncate, which does the same for display
// columns.
func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// extractTimestampFromLine extracts a timestamp from a JSONL line using fast
// string matching rather than full JSON parsing.
func extractTimestampFromLine(line string) time.Time {
	const prefix = `"timestamp":"`
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return time.Time{}
	}
	start := idx + len(prefix)
	end := strings.IndexByte(line[start:], '"')
	if end < 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, line[start:start+end])
	if err != nil {
		return time.Time{}
	}
	return ts
}

// GetDateGroup returns a human-readable date group for a session.
func GetDateGroup(t time.Time) string {
	return getDateGroupAt(time.Now(), t)
}

// getDateGroupAt is GetDateGroup with the clock passed in, so the boundaries
// can be tested.
//
// Both times are moved into the reader's location before anything is compared.
// Claude writes RFC3339 timestamps in UTC, so taking the calendar date of each
// in its own location subtracted two different midnights and shifted every
// heading by a day for anyone east of UTC. Counting whole days out of an hour
// difference has its own failure: a spring-forward day is 23 hours long, and
// 23/24 truncates to zero, which labels yesterday "Today". Comparing calendar
// dates avoids both.
func getDateGroupAt(now, t time.Time) string {
	loc := now.Location()
	local := t.In(loc)

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	sessionDate := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	switch {
	case sessionDate.Equal(today):
		return "Today"
	case sessionDate.Equal(today.AddDate(0, 0, -1)):
		return "Yesterday"
	default:
		return local.Format("Jan 2")
	}
}
