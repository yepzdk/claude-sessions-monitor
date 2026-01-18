package session

import (
	"encoding/json"
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
	LogFile      string        `json:"-"`
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

// DiscoverHistory finds all sessions from the past N days
func DiscoverHistory(days int) ([]HistorySession, error) {
	projectsDir := ClaudeProjectsDir()
	cutoff := time.Now().AddDate(0, 0, -days)

	var sessions []HistorySession

	// Find all sessions-index.json files
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

			// Parse timestamps
			startTime, err := time.Parse(time.RFC3339, entry.Created)
			if err != nil {
				continue
			}

			// Filter by date range
			if startTime.Before(cutoff) {
				continue
			}

			endTime, err := time.Parse(time.RFC3339, entry.Modified)
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
	// /Users/username/Projects/org/project -> org/project
	if idx := strings.Index(fullPath, "/Projects/"); idx != -1 {
		return fullPath[idx+len("/Projects/"):]
	}

	// Fallback: just use the last two path components
	parts := strings.Split(fullPath, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}

	return filepath.Base(fullPath)
}

// GetDateGroup returns a human-readable date group for a session
func GetDateGroup(t time.Time) string {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	sessionDate := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())

	days := int(today.Sub(sessionDate).Hours() / 24)

	switch days {
	case 0:
		return "Today"
	case 1:
		return "Yesterday"
	default:
		return t.Format("Jan 2")
	}
}
