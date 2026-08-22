package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// timelineFixture writes a log file where a session's user turns are a small
// fraction of the entries, and points HOME at it so ValidateLogFilePath accepts
// the path. Returns the file to pass as the file parameter.
func timelineFixture(t *testing.T, userTurns, assistantTurns int) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".claude", "projects", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var b strings.Builder
	for i := 0; i < userTurns; i++ {
		b.WriteString(`{"type":"user","timestamp":"2025-01-01T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n")
	}
	for i := 0; i < assistantTurns; i++ {
		b.WriteString(`{"type":"assistant","timestamp":"2025-01-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"yo"}]}}` + "\n")
	}

	logFile := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(logFile, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return logFile
}

func timelineResponse(t *testing.T, logFile, typeParam string) (entries []map[string]any, total int, echoedType string) {
	t.Helper()

	url := "/api/sessions/timeline?file=" + logFile + "&offset=0&limit=100"
	if typeParam != "" {
		url += "&type=" + typeParam
	}

	rec := httptest.NewRecorder()
	handleTimeline(rec, httptest.NewRequest(http.MethodGet, url, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var body struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
		Type    string           `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Entries, body.Total, body.Type
}

// The type parameter decides what reaches the parser, so the whitelist is the
// part of the filter the parser's own tests cannot cover.
func TestHandleTimelineTypeFilter(t *testing.T) {
	logFile := timelineFixture(t, 3, 20)

	t.Run("no type returns every entry", func(t *testing.T) {
		entries, total, echoed := timelineResponse(t, logFile, "")
		if total != 23 {
			t.Errorf("total = %d, want 23", total)
		}
		if len(entries) != 23 {
			t.Errorf("len(entries) = %d, want 23", len(entries))
		}
		if echoed != "" {
			t.Errorf("type = %q, want empty", echoed)
		}
	})

	t.Run("known type filters and is echoed back", func(t *testing.T) {
		entries, total, echoed := timelineResponse(t, logFile, "user")
		if total != 3 {
			t.Errorf("total = %d, want 3", total)
		}
		if echoed != "user" {
			t.Errorf("type = %q, want %q", echoed, "user")
		}
		for _, e := range entries {
			if e["type"] != "user" {
				t.Errorf("entry type = %v, want user", e["type"])
			}
		}
	})

	// An unrecognised type must not filter every entry out: an empty timeline
	// reads as a broken log rather than a bad query. The echoed type is how a
	// caller can tell its filter was dropped.
	t.Run("unknown type falls back to unfiltered", func(t *testing.T) {
		_, total, echoed := timelineResponse(t, logFile, "bogus")
		if total != 23 {
			t.Errorf("total = %d, want 23 (unfiltered)", total)
		}
		if echoed != "" {
			t.Errorf("type = %q, want empty", echoed)
		}
	})

	t.Run("summary is an accepted type", func(t *testing.T) {
		_, _, echoed := timelineResponse(t, logFile, "summary")
		if echoed != "summary" {
			t.Errorf("type = %q, want %q", echoed, "summary")
		}
	})
}
