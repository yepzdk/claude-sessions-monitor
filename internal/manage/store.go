package manage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Agent records a managed agent spawned by `csm manage`. Persisted as one
// JSON file per agent under managedDir(). The state file is the source of
// truth for which agents csm manages; the CSM_MANAGED env var on the
// spawned process is only a secondary signal.
type Agent struct {
	Project    string    `json:"project"`    // base name of the source repo
	Name       string    `json:"name"`       // agent-1, agent-2, ...
	Repo       string    `json:"repo"`       // toplevel of the source repo
	Worktree   string    `json:"worktree"`   // worktree checkout path
	Branch     string    `json:"branch"`     // csm/<project>/<agent>
	TmuxWindow string    `json:"tmuxWindow"` // tmux window name (== Name)
	CreatedAt  time.Time `json:"createdAt"`
}

// managedDirFn is overridable in tests.
var managedDirFn = defaultManagedDir

func defaultManagedDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude-monitor", "managed"), nil
}

// ManagedDir returns the directory where managed-agent records are persisted.
func ManagedDir() (string, error) {
	return managedDirFn()
}

func agentFileName(project, name string) string {
	return fmt.Sprintf("%s-%s.json", project, name)
}

// SaveAgent persists an agent record using an atomic temp-write-then-rename.
func SaveAgent(a Agent) error {
	dir, err := ManagedDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create managed dir: %w", err)
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(dir, agentFileName(a.Project, a.Name))
	tmp, err := os.CreateTemp(dir, agentFileName(a.Project, a.Name)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// ListAgents returns all persisted agent records. A missing managed dir is
// not an error — it just means no agents have been spawned yet.
func ListAgents() ([]Agent, error) {
	dir, err := ManagedDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var agents []Agent
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip unreadable records rather than failing the whole list
		}
		var a Agent
		if err := json.Unmarshal(data, &a); err != nil {
			continue
		}
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].CreatedAt.Before(agents[j].CreatedAt)
	})
	return agents, nil
}

// nextAgentName returns the next free agent-N name for a project, based on
// the highest existing index. Names are not reused even after cleanup
// within the same csm session lifetime — gaps are fine.
func nextAgentName(project string) (string, error) {
	agents, err := ListAgents()
	if err != nil {
		return "", err
	}
	max := 0
	for _, a := range agents {
		if a.Project != project {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(a.Name, "agent-%d", &n); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("agent-%d", max+1), nil
}
