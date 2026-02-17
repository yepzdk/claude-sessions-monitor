package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Status represents the current state of a Claude session
type Status string

const (
	StatusWorking      Status = "Working"
	StatusNeedsInput   Status = "Needs Input"
	StatusWaiting      Status = "Waiting"
	StatusIdle         Status = "Idle"
	StatusInactive     Status = "Inactive"
)

// Session represents a Claude Code session
type Session struct {
	Project        string    `json:"project"`
	Status         Status    `json:"status"`
	LastActivity   time.Time `json:"last_activity"`
	Task           string    `json:"task"`
	Summary        string    `json:"summary,omitempty"`
	LastMessage    string    `json:"last_message,omitempty"`
	LogFile        string    `json:"-"`
	ProjectPath    string    `json:"-"`                        // Full path to the project directory
	IsDesktop      bool      `json:"is_desktop,omitempty"`     // True if session appears to be from desktop app
	IsGhost        bool      `json:"is_ghost,omitempty"`       // True if process running but log is stale
	GhostPID       int       `json:"ghost_pid,omitempty"`      // PID of the ghost process (for killing)
	GitBranch      string    `json:"git_branch,omitempty"`     // Current git branch
	HasUnsandboxed bool      `json:"has_unsandboxed,omitempty"` // True if any command bypassed sandbox
	ContextPercent float64   `json:"context_percent,omitempty"` // Percentage of context window used
	ContextTokens  int       `json:"context_tokens,omitempty"`  // Total input tokens from last usage entry
}

// RunningProcess represents a Claude process with its PID and working directory
type RunningProcess struct {
	PID int
	Dir string // Encoded directory name
}

// LogEntry represents a single line in the JSONL log
type LogEntry struct {
	Type      string    `json:"type"`
	Subtype   string    `json:"subtype,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Message   *Message  `json:"message,omitempty"`
	Summary   string    `json:"summary,omitempty"` // For type: "summary" entries
	GitBranch string    `json:"gitBranch,omitempty"`
}

// Message represents the message field in a log entry
type Message struct {
	Role    string        `json:"role,omitempty"`
	Model   string        `json:"model,omitempty"`
	Content []ContentItem `json:"content,omitempty"`
	Usage   *Usage        `json:"usage,omitempty"`
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
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`  // For tool_use
	Input json.RawMessage `json:"input,omitempty"` // For tool_use inputs
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
func getRunningClaudeDirs() map[string]int {
	dirs := make(map[string]int)

	// Use ps to get Claude process IDs (more reliable than pgrep)
	cmd := exec.Command("sh", "-c", "ps ax -o pid,comm | grep '[c]laude$' | awk '{print $1}'")
	output, err := cmd.Output()
	if err != nil {
		return dirs
	}

	pids := strings.Fields(string(output))
	for _, pidStr := range pids {
		pid := 0
		fmt.Sscanf(pidStr, "%d", &pid)
		if pid == 0 {
			continue
		}

		// Get cwd for each process using lsof
		lsofCmd := exec.Command("lsof", "-p", pidStr)
		lsofOutput, err := lsofCmd.Output()
		if err != nil {
			continue
		}

		// Parse lsof output to find cwd
		lines := bytes.Split(lsofOutput, []byte("\n"))
		for _, line := range lines {
			if bytes.Contains(line, []byte(" cwd ")) {
				fields := bytes.Fields(line)
				if len(fields) >= 9 {
					// Last field is the path
					path := string(fields[len(fields)-1])
					// Convert to encoded format (same as project directory names)
					encoded := encodeProjectPath(path)
					dirs[encoded] = pid
				}
			}
		}
	}

	return dirs
}

// encodeProjectPath converts a filesystem path to the encoded directory name format
func encodeProjectPath(path string) string {
	// /Users/username/Projects/org/project -> -Users-username-Projects-org-project
	// Also replace dots with dashes (Claude's encoding scheme)
	encoded := strings.ReplaceAll(path, "/", "-")
	encoded = strings.ReplaceAll(encoded, ".", "-")
	return encoded
}

// isDesktopSession checks if the project path appears to be from the desktop app
// Desktop app sessions typically have cwd at the home directory (e.g., -Users-username)
func isDesktopSession(projectName string) bool {
	// NOTE: Desktop detection is disabled because the previous heuristic
	// (home directory = desktop) was unreliable - terminal sessions can also
	// be started from the home directory.
	// TODO: Find a more reliable detection method (e.g., check parent process)
	_ = projectName
	return false
}

// Discover finds all active Claude sessions
func Discover() ([]Session, error) {
	projectsDir, err := ClaudeProjectsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}

	// Get directories where Claude is currently running
	runningDirs := getRunningClaudeDirs()

	var sessions []Session

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip hidden directories
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		projectDir := filepath.Join(projectsDir, entry.Name())
		logFile, err := findMostRecentLog(projectDir)
		if err != nil || logFile == "" {
			continue
		}

		session, err := parseSession(entry.Name(), logFile, runningDirs)
		if err != nil {
			continue
		}

		// Skip ghost processes when they have no recent activity and are truly stale
		sessions = append(sessions, session)
	}

	// Sort by status priority, then by last activity
	sort.Slice(sessions, func(i, j int) bool {
		// Priority: Working > NeedsInput > Waiting > Idle > Inactive
		pi, pj := statusPriority(sessions[i].Status), statusPriority(sessions[j].Status)
		if pi != pj {
			return pi < pj
		}
		return sessions[i].LastActivity.After(sessions[j].LastActivity)
	})

	return sessions, nil
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
	case StatusIdle:
		return 3
	case StatusInactive:
		return 4
	default:
		return 5
	}
}

// findMostRecentLog finds the most recently modified .jsonl file in a directory
func findMostRecentLog(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	// Track both most recent non-empty and most recent overall
	var mostRecent string
	var mostRecentTime time.Time
	var newestOverall string
	var newestOverallTime time.Time

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

		filePath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Track newest file regardless of size (for detecting fresh sessions)
		if info.ModTime().After(newestOverallTime) {
			newestOverallTime = info.ModTime()
			newestOverall = filePath
		}

		// Track newest non-empty file
		if info.Size() > 0 && info.ModTime().After(mostRecentTime) {
			mostRecentTime = info.ModTime()
			mostRecent = filePath
		}
	}

	// If there's a newer empty file, a fresh session just started;
	// return it so parseSession sees 0 entries and shows "-" context
	if newestOverall != mostRecent && newestOverallTime.After(mostRecentTime) {
		return newestOverall, nil
	}

	return mostRecent, nil
}

// parseSession parses a session from its log file
func parseSession(projectName, logFile string, runningDirs map[string]int) (Session, error) {
	session := Session{
		Project:     decodeProjectName(projectName),
		LogFile:     logFile,
		Status:      StatusInactive, // Default to inactive
		ProjectPath: projectName,    // Store the encoded name for matching
		IsDesktop:   isDesktopSession(projectName),
	}

	// Check if Claude is running in this project directory
	// runningDirs keys are in the same encoded format as projectName
	pid, isRunning := runningDirs[projectName]

	// Get file modification time as fallback for last activity
	info, err := os.Stat(logFile)
	if err != nil {
		return session, err
	}
	session.LastActivity = info.ModTime()

	// Read last N lines of the file to determine status
	entries, err := readLastEntries(logFile, 100)
	if err != nil {
		return session, nil // Return with defaults
	}

	if len(entries) == 0 {
		return session, nil
	}

	// Extract summary from the log file (scans entire file)
	session.Summary = extractSummary(logFile)

	// Extract last assistant message text
	session.LastMessage = extractLastAssistantMessage(entries)

	// Extract git branch (use most recent non-empty)
	session.GitBranch = extractGitBranch(entries)

	// Detect if any commands ran without sandbox
	session.HasUnsandboxed = detectUnsandboxedCommands(entries)

	// Extract context usage
	session.ContextPercent, session.ContextTokens = extractContextUsage(entries)

	// Determine status from log entries
	session.Status, session.Task, session.IsGhost = determineStatus(entries, isRunning)

	// Store PID for all running sessions (used by --kill-ghosts)
	if isRunning && pid > 0 {
		session.GhostPID = pid
	}

	// Get actual last activity timestamp from entries
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].Timestamp.IsZero() {
			session.LastActivity = entries[i].Timestamp
			break
		}
	}

	return session, nil
}

// extractSummary reads the entire file to find the most recent summary entry
// Summaries are typically at the beginning of the file, so we need to scan it all
func extractSummary(logFile string) string {
	file, err := os.Open(logFile)
	if err != nil {
		return ""
	}
	defer file.Close()

	var lastSummary string
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Quick check before full JSON parse
		if !strings.Contains(line, `"type":"summary"`) {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if entry.Type == "summary" && entry.Summary != "" {
			lastSummary = entry.Summary
		}
	}

	return lastSummary
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

// extractContextUsage extracts context usage from the last assistant entry with usage data
// Returns the percentage of context window used and total input tokens.
// Only considers entries after the most recent compact/microcompact boundary,
// since context is reset during compaction.
func extractContextUsage(entries []LogEntry) (float64, int) {
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
		totalTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
		if totalTokens == 0 {
			continue
		}

		percent := float64(totalTokens) / float64(DefaultContextWindow) * 100
		return percent, totalTokens
	}

	return 0, 0
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

// readLastEntries reads the last N valid JSON entries from a JSONL file
func readLastEntries(filePath string, count int) ([]LogEntry, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(file)
	// Increase buffer size for very long lines (some entries can be several MB)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // 10MB max

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		entries = append(entries, entry)
	}

	// Return last N entries
	if len(entries) > count {
		entries = entries[len(entries)-count:]
	}

	return entries, scanner.Err()
}

// DefaultContextWindow is the context window size for Claude models (200K tokens)
const DefaultContextWindow = 200000

// GhostThreshold is the duration after which a running process with no log activity
// is considered a ghost (orphaned) process
const GhostThreshold = 10 * time.Minute

// determineStatus analyzes log entries to determine session status
// Returns: status, task description, and whether this is a ghost process
func determineStatus(entries []LogEntry, isRunning bool) (Status, string, bool) {
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
		}

		// Stop once we have all we need
		if lastAssistant != nil && lastUser != nil && lastSystem != nil {
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
		for _, content := range lastAssistant.Message.Content {
			if content.Type == "tool_use" {
				// Check if there's a corresponding tool_result after
				hasToolResult := false
				if lastUser != nil && lastUser.Timestamp.After(lastAssistant.Timestamp) {
					for _, uc := range lastUser.Message.Content {
						if uc.Type == "tool_result" {
							hasToolResult = true
							// Tool was approved, check if still working
							if lastSystem != nil && lastSystem.Timestamp.After(lastUser.Timestamp) {
								return StatusWaiting, "-", false
							}
							return StatusWorking, "Processing...", false
						}
					}
				}
				if !hasToolResult {
					hasPendingToolUse = true
					pendingToolName = content.Name
				}
				break
			}
		}
	}

	// If there's a pending tool_use, session is waiting for input (not ghost/idle)
	if hasPendingToolUse {
		return StatusNeedsInput, "Using: " + pendingToolName, false
	}

	// If process is running but log is stale, it's Waiting (not ghost)
	// The user may be away or thinking - this is a valid active session
	// Ghost detection is only for --kill-ghosts to find truly orphaned processes
	if time.Since(lastTimestamp) > 5*time.Minute {
		return StatusWaiting, "-", false
	}

	// Check if turn completed (system message with turn_duration)
	if lastSystem != nil {
		if lastAssistant == nil || lastSystem.Timestamp.After(lastAssistant.Timestamp) {
			return StatusWaiting, "-", false
		}
	}

	// If assistant is recent and no turn_duration, it's working
	if lastAssistant != nil {
		task := extractTask(lastAssistant)
		if time.Since(lastAssistant.Timestamp) < 30*time.Second {
			return StatusWorking, task, false
		}
	}

	return StatusWaiting, "-", false
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

// FindGhostProcesses returns a list of potentially orphaned Claude processes
// Uses a 1-hour threshold to identify processes with no recent log activity
func FindGhostProcesses() ([]GhostProcess, error) {
	sessions, err := Discover()
	if err != nil {
		return nil, err
	}

	var ghosts []GhostProcess
	for _, s := range sessions {
		// Only consider sessions with a running process
		if s.GhostPID == 0 {
			continue
		}
		// Check if log is stale (> 1 hour since last activity)
		age := time.Since(s.LastActivity)
		if age > time.Hour {
			ghosts = append(ghosts, GhostProcess{
				PID:     s.GhostPID,
				Project: s.Project,
				Age:     age,
			})
		}
	}

	return ghosts, nil
}

// KillGhostProcesses terminates all ghost Claude processes
// Returns the number of processes killed and any errors
func KillGhostProcesses() ([]GhostProcess, error) {
	ghosts, err := FindGhostProcesses()
	if err != nil {
		return nil, err
	}

	var killed []GhostProcess
	for _, ghost := range ghosts {
		// Send SIGTERM to gracefully terminate the process
		process, err := os.FindProcess(ghost.PID)
		if err != nil {
			continue
		}

		err = process.Signal(syscall.SIGTERM)
		if err != nil {
			// Process might already be gone
			continue
		}

		killed = append(killed, ghost)
	}

	return killed, nil
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

// GetGhostPIDs returns just the PIDs of ghost processes (for simple listing)
func GetGhostPIDs() ([]int, error) {
	ghosts, err := FindGhostProcesses()
	if err != nil {
		return nil, err
	}

	pids := make([]int, len(ghosts))
	for i, g := range ghosts {
		pids[i] = g.PID
	}
	return pids, nil
}
