package session

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// ClaudeStatus holds the response from the Claude status page API.
type ClaudeStatus struct {
	Available   bool   `json:"available"`
	Indicator   string `json:"indicator"`
	Description string `json:"description"`
	Error       string `json:"error,omitempty"`
}

var claudeStatusCache struct {
	sync.Mutex
	result    *ClaudeStatus
	fetchedAt time.Time
}

const claudeStatusCacheTTL = 60 * time.Second

// FetchClaudeStatus queries the Claude status page API for service health.
// Results are cached for 60 seconds to avoid excessive API calls.
func FetchClaudeStatus() *ClaudeStatus {
	claudeStatusCache.Lock()
	defer claudeStatusCache.Unlock()

	if claudeStatusCache.result != nil && time.Since(claudeStatusCache.fetchedAt) < claudeStatusCacheTTL {
		return claudeStatusCache.result
	}

	result := fetchClaudeStatusUncached()
	claudeStatusCache.result = result
	claudeStatusCache.fetchedAt = time.Now()
	return result
}

func fetchClaudeStatusUncached() *ClaudeStatus {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://status.claude.com/api/v2/status.json")
	if err != nil {
		return &ClaudeStatus{Available: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &ClaudeStatus{Available: false, Error: err.Error()}
	}

	if resp.StatusCode != http.StatusOK {
		return &ClaudeStatus{Available: false, Error: "API returned " + resp.Status}
	}

	var raw struct {
		Status struct {
			Indicator   string `json:"indicator"`
			Description string `json:"description"`
		} `json:"status"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return &ClaudeStatus{Available: false, Error: "failed to parse status response"}
	}

	return &ClaudeStatus{
		Available:   true,
		Indicator:   raw.Status.Indicator,
		Description: raw.Status.Description,
	}
}
