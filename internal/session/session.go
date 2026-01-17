package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Status represents the current state of a Claude session
type Status string

const (
	StatusWorking      Status = "Working"
	StatusNeedsInput   Status = "Needs Input"
	StatusWaiting      Status = "Waiting"
	StatusIdle         Status = "Idle"
)

// Session represents a Claude Code session
type Session struct {
	Project      string    `json:"project"`
	Status       Status    `json:"status"`
	LastActivity time.Time `json:"last_activity"`
	Task         string    `json:"task"`
	LogFile      string    `json:"-"`
}

// LogEntry represents a single line in the JSONL log
type LogEntry struct {
	Type      string    `json:"type"`
	Subtype   string    `json:"subtype,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Message   *Message  `json:"message,omitempty"`
}

// Message represents the message field in a log entry
type Message struct {
	Role    string        `json:"role,omitempty"`
	Content []ContentItem `json:"content,omitempty"`
}

// ContentItem represents an item in the content array
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"` // For tool_use
}

// ClaudeProjectsDir returns the path to the Claude projects directory
func ClaudeProjectsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// Discover finds all active Claude sessions
func Discover() ([]Session, error) {
	projectsDir := ClaudeProjectsDir()

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}

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

		session, err := parseSession(entry.Name(), logFile)
		if err != nil {
			continue
		}

		sessions = append(sessions, session)
	}

	// Sort by last activity (most recent first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastActivity.After(sessions[j].LastActivity)
	})

	return sessions, nil
}

// findMostRecentLog finds the most recently modified .jsonl file in a directory
func findMostRecentLog(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var mostRecent string
	var mostRecentTime time.Time

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

		if info.ModTime().After(mostRecentTime) {
			mostRecentTime = info.ModTime()
			mostRecent = filePath
		}
	}

	return mostRecent, nil
}

// parseSession parses a session from its log file
func parseSession(projectName, logFile string) (Session, error) {
	session := Session{
		Project: decodeProjectName(projectName),
		LogFile: logFile,
		Status:  StatusIdle,
	}

	// Get file modification time as fallback for last activity
	info, err := os.Stat(logFile)
	if err != nil {
		return session, err
	}
	session.LastActivity = info.ModTime()

	// Read last N lines of the file to determine status
	entries, err := readLastEntries(logFile, 50)
	if err != nil {
		return session, nil // Return with defaults
	}

	if len(entries) == 0 {
		return session, nil
	}

	// Determine status from log entries
	session.Status, session.Task = determineStatus(entries)

	// Get actual last activity timestamp from entries
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].Timestamp.IsZero() {
			session.LastActivity = entries[i].Timestamp
			break
		}
	}

	return session, nil
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
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

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

// determineStatus analyzes log entries to determine session status
func determineStatus(entries []LogEntry) (Status, string) {
	if len(entries) == 0 {
		return StatusIdle, "-"
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

	// Check for idle (5+ minutes since last activity)
	if time.Since(lastTimestamp) > 5*time.Minute {
		return StatusIdle, "-"
	}

	// Check if assistant ended with tool_use (needs approval)
	if lastAssistant != nil && lastAssistant.Message != nil {
		for _, content := range lastAssistant.Message.Content {
			if content.Type == "tool_use" {
				// Check if there's a corresponding tool_result after
				if lastUser != nil && lastUser.Timestamp.After(lastAssistant.Timestamp) {
					for _, uc := range lastUser.Message.Content {
						if uc.Type == "tool_result" {
							// Tool was approved, check if still working
							if lastSystem != nil && lastSystem.Timestamp.After(lastUser.Timestamp) {
								return StatusWaiting, "-"
							}
							return StatusWorking, "Processing..."
						}
					}
				}
				return StatusNeedsInput, "Using: " + content.Name
			}
		}
	}

	// Check if turn completed (system message with turn_duration)
	if lastSystem != nil {
		if lastAssistant == nil || lastSystem.Timestamp.After(lastAssistant.Timestamp) {
			return StatusWaiting, "-"
		}
	}

	// If assistant is recent and no turn_duration, it's working
	if lastAssistant != nil {
		task := extractTask(lastAssistant)
		if time.Since(lastAssistant.Timestamp) < 30*time.Second {
			return StatusWorking, task
		}
	}

	return StatusWaiting, "-"
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
