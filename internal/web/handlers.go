package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleSessions returns active (non-inactive) sessions as JSON
func handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := session.Discover()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	active := make([]session.Session, 0, len(sessions))
	for _, s := range sessions {
		if s.Status != session.StatusInactive {
			active = append(active, s)
		}
	}
	writeJSON(w, active)
}

// handleHistory returns past sessions as JSON
func handleHistory(w http.ResponseWriter, r *http.Request) {
	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}

	sessions, err := session.DiscoverHistory(days)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sessions == nil {
		sessions = []session.HistorySession{}
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

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	entries, total, err := session.ParseTimeline(filePath, offset, limit)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{
		"entries": entries,
		"total":   total,
		"offset":  offset,
		"limit":   limit,
	})
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
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, metrics)
}
