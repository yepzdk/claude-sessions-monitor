package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Subagent represents a Task/Agent subagent spawned by a parent session.
//
// Claude Code writes each subagent to its own log under
// <project>/<session-uuid>/subagents/agent-<id>.jsonl, with a sibling
// agent-<id>.meta.json describing what it was spawned to do. The parent's own
// log stays silent for the whole run, so a subagent is the only place its
// activity is visible.
type Subagent struct {
	ID           string    `json:"id"`
	AgentType    string    `json:"agent_type,omitempty"`
	Description  string    `json:"description,omitempty"`
	Model        string    `json:"model,omitempty"`
	Status       Status    `json:"status"`
	Task         string    `json:"task,omitempty"`
	LastActivity time.Time `json:"last_activity"`
	LogFile      string    `json:"log_file"`

	// Blocking reports that the parent's tool_use for this subagent has no
	// tool_result yet, i.e. the parent turn cannot continue until it finishes.
	// Background agents are launched with an immediate result and are not
	// blocking even while they keep running.
	Blocking bool `json:"blocking,omitempty"`
}

// subagentActiveWindow bounds how long after its last log write a subagent is
// still considered live. It is deliberately more generous than
// recentActivityWindow: a subagent can sit silent for minutes inside a single
// slow tool call (a long build, a large search) and is still working.
const subagentActiveWindow = 5 * time.Minute

// subagentMeta is the sidecar agent-<id>.meta.json written when a subagent spawns.
type subagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	Model       string `json:"model"`
}

// subagentsDir returns the directory holding a session's subagent logs.
func subagentsDir(logFile string) string {
	return filepath.Join(filepath.Dir(logFile), sessionIDFromLogFile(logFile), "subagents")
}

// pendingToolUseIDs returns the ids of tool_use blocks that have no matching
// tool_result yet. Used to tell a blocking subagent from a background one.
func pendingToolUseIDs(entries []LogEntry) map[string]bool {
	pending := map[string]bool{}
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		for _, c := range entry.Message.Content {
			switch c.Type {
			case "tool_use":
				if c.ID != "" {
					pending[c.ID] = true
				}
			case "tool_result":
				delete(pending, c.ToolUseID)
			}
		}
	}
	return pending
}

// discoverSubagents returns the subagents of a session that are still live.
//
// Finished subagents are omitted: their logs remain on disk forever, and
// listing every agent a long session ever spawned would bury the running ones.
func discoverSubagents(logFile string, pending map[string]bool, isRunning bool) []Subagent {
	if !isRunning {
		return nil
	}

	dir := subagentsDir(logFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var subagents []Subagent
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		path := filepath.Join(dir, name)
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		meta := readSubagentMeta(dir, id)

		blocking := meta.ToolUseID != "" && pending[meta.ToolUseID]
		// A blocking agent stays listed however long it has been quiet — the
		// parent is provably still waiting on it.
		if !blocking && time.Since(info.ModTime()) > subagentActiveWindow {
			continue
		}

		sa := Subagent{
			ID:           id,
			AgentType:    meta.AgentType,
			Description:  meta.Description,
			Model:        meta.Model,
			Status:       StatusWorking,
			LastActivity: info.ModTime(),
			LogFile:      path,
			Blocking:     blocking,
		}

		// A partial parse is still worth reading. cachedParseLogFile returns the
		// non-fatal error alongside whatever it recovered -- a log with one line
		// over maxLogLineBytes after N good entries -- and gating on err == nil
		// discarded those entries. Since the error is cached with the parse and
		// the oversized line never leaves the file, that discarded this
		// subagent's Task and LastActivity on every tick, not just the first.
		// A fatal parse returns a zero parsedLog, and the checks below skip it.
		pl, _ := cachedParseLogFile(path, info.ModTime(), info.Size(), 100)
		if pl.lastMessage != "" {
			sa.Task = pl.lastMessage
		}
		if !pl.lastEntryTime.IsZero() {
			sa.LastActivity = pl.lastEntryTime
		}

		subagents = append(subagents, sa)
	}

	sort.Slice(subagents, func(i, j int) bool {
		return subagents[i].LastActivity.After(subagents[j].LastActivity)
	})

	return subagents
}

// readSubagentMeta loads agent-<id>.meta.json, returning a zero value if the
// sidecar is missing or unreadable — the log alone is still worth showing.
func readSubagentMeta(dir, id string) subagentMeta {
	var meta subagentMeta
	data, err := os.ReadFile(filepath.Join(dir, "agent-"+id+".meta.json"))
	if err != nil {
		return meta
	}
	_ = json.Unmarshal(data, &meta)
	return meta
}

// rollUpSubagentStatus reports whether a parent status should be replaced by
// Working because a subagent is running under it.
//
// Needs Input is never overridden: it means a tool is sitting on an approval
// prompt, and burying that behind "Working" would leave the user waiting on a
// session that is really waiting on them. Working needs no change.
func rollUpSubagentStatus(status Status) bool {
	return status == StatusWaiting
}

// subagentTask describes what a parent's subagents are doing, used when the
// parent's own log offers no more recent task text.
func subagentTask(subagents []Subagent) string {
	blocking := 0
	for _, s := range subagents {
		if s.Blocking {
			blocking++
		}
	}
	if blocking > 0 {
		return "Waiting on " + pluralAgents(blocking)
	}
	return pluralAgents(len(subagents)) + " running"
}

func pluralAgents(n int) string {
	if n == 1 {
		return "1 agent"
	}
	return strconv.Itoa(n) + " agents"
}

// subagentLabel is the display name for a subagent: its agent type, falling
// back to a short id when the metadata sidecar is missing.
func (s Subagent) Label() string {
	if s.AgentType != "" {
		return s.AgentType
	}
	if len(s.ID) > 8 {
		return s.ID[:8]
	}
	return s.ID
}
