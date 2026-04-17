package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// liveRetention is how long a stopped session remains visible in the live view.
const liveRetention = time.Hour

// filterLiveSessions returns active sessions plus recently-stopped sessions
// whose LastActivity is within the liveRetention window.
func filterLiveSessions(all []session.Session) []session.Session {
	cutoff := time.Now().Add(-liveRetention)
	result := make([]session.Session, 0, len(all))
	for _, s := range all {
		if s.Status != session.StatusInactive {
			result = append(result, s)
		} else if s.LastActivity.After(cutoff) {
			result = append(result, s)
		}
	}
	return result
}

// handleSessions returns active and recently-stopped sessions as JSON
func handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := session.Discover()
	if err != nil {
		writeError(w, "failed to discover sessions", http.StatusInternalServerError)
		return
	}
	writeJSON(w, filterLiveSessions(sessions))
}

// handleHistory returns past sessions as JSON, merging index-based history
// with inactive sessions from Discover() so they always appear somewhere.
func handleHistory(w http.ResponseWriter, r *http.Request) {
	const maxDays = 365
	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
			if days > maxDays {
				days = maxDays
			}
		}
	}

	sessions, err := session.DiscoverHistory(days)
	if err != nil {
		writeError(w, "failed to load history", http.StatusInternalServerError)
		return
	}
	if sessions == nil {
		sessions = []session.HistorySession{}
	}

	// Merge inactive sessions from Discover() so they are visible in history
	liveSessions, err := session.Discover()
	if err == nil {
		// Track log files already in history to avoid duplicates
		seen := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			seen[s.LogFile] = true
		}

		cutoff := time.Now().AddDate(0, 0, -days)
		for _, s := range liveSessions {
			if s.Status != session.StatusInactive {
				continue
			}
			if s.LastActivity.Before(cutoff) {
				continue
			}
			if seen[s.LogFile] {
				continue
			}

			// Enrich with stats from the JSONL file
			msgCount, start, end, extractedBranch, firstPrompt, _, _ := session.QuickSessionStats(s.LogFile)
			if start.IsZero() {
				start = s.LastActivity
			}
			if end.IsZero() {
				end = s.LastActivity
			}

			// Prefer live session's git branch, fall back to extracted
			branch := s.GitBranch
			if branch == "" {
				branch = extractedBranch
			}

			sessions = append(sessions, session.HistorySession{
				Project:      s.Project,
				GitBranch:    branch,
				FirstPrompt:  firstPrompt,
				StartTime:    start,
				EndTime:      end,
				Duration:     end.Sub(start),
				MessageCount: msgCount,
				LastMessage:  s.LastMessage,
				LogFile:      s.LogFile,
			})
		}

		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].StartTime.After(sessions[j].StartTime)
		})
	}

	writeJSON(w, sessions)
}

// handleTimeline returns paginated message timeline for a log file
func handleTimeline(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		writeError(w, "file parameter is required", http.StatusBadRequest)
		return
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	const maxLimit = 500
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > maxLimit {
				limit = maxLimit
			}
		}
	}

	entries, total, err := session.ParseTimeline(filePath, offset, limit)
	if err != nil {
		writeError(w, "failed to parse timeline", http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{
		"entries": entries,
		"total":   total,
		"offset":  offset,
		"limit":   limit,
	})
}

// handleUsage returns local token usage stats and API quota as JSON.
func handleUsage(w http.ResponseWriter, r *http.Request) {
	usage := session.ComputeUsage()
	apiQuota := session.FetchAPIQuota()
	writeJSON(w, map[string]any{"local": usage, "api_quota": apiQuota})
}

// handleClaudeStatus returns the current Claude service status as JSON.
func handleClaudeStatus(w http.ResponseWriter, r *http.Request) {
	status := session.FetchClaudeStatus()
	writeJSON(w, status)
}

// handleMetrics returns aggregated metrics for a log file
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		writeError(w, "file parameter is required", http.StatusBadRequest)
		return
	}

	metrics, err := session.ParseMetrics(filePath)
	if err != nil {
		writeError(w, "failed to parse metrics", http.StatusBadRequest)
		return
	}

	writeJSON(w, metrics)
}
