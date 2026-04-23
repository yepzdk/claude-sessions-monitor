package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// originStoreDirFn is overridable in tests.
var originStoreDirFn = defaultOriginStoreDir

func defaultOriginStoreDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude-monitor", "origins"), nil
}

// OriginStoreDir returns the directory where per-session origin snapshots are persisted.
func OriginStoreDir() (string, error) {
	return originStoreDirFn()
}

// LoadOrigin reads the cached origin for the given session UUID.
// Returns (Origin{}, false) when no cache exists or on any read/parse error.
func LoadOrigin(sessionID string) (Origin, bool) {
	if sessionID == "" {
		return Origin{}, false
	}
	dir, err := OriginStoreDir()
	if err != nil {
		return Origin{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, sessionID+".json"))
	if err != nil {
		return Origin{}, false
	}
	var o Origin
	if err := json.Unmarshal(data, &o); err != nil {
		return Origin{}, false
	}
	if o.IsZero() {
		return Origin{}, false
	}
	return o, true
}

// SaveOrigin persists a detected origin for a session. Empty sessionIDs and
// zero-valued origins are skipped (nothing useful to cache).
func SaveOrigin(sessionID string, o Origin) error {
	if sessionID == "" || o.IsZero() {
		return nil
	}
	dir, err := OriginStoreDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create origin store dir: %w", err)
	}
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, sessionID+".json")
	tmp, err := os.CreateTemp(dir, sessionID+".*.json.tmp")
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
