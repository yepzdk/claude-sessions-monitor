//go:build linux

package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// GetOAuthToken reads the token from ~/.claude/.credentials.json.
func GetOAuthToken() (*OAuthToken, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locating the home directory: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		// No file at all is the normal state for someone who has not signed
		// in, or who uses an API key. Reporting that as an errno with the
		// user's home path in it reads like a fault and sends them looking for
		// a broken install.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("not signed in to Claude Code")
		}
		// The error from ReadFile already names the file, so the wrap does not
		// repeat the path.
		return nil, fmt.Errorf("reading Claude Code credentials: %w", err)
	}
	return parseCredentials(data)
}
