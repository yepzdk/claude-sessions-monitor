package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OAuthToken holds the Claude Code OAuth credentials.
type OAuthToken struct {
	AccessToken string `json:"accessToken"`
}

// keychainServiceName is the service name Claude Code uses in the macOS Keychain.
const keychainServiceName = "Claude Code-credentials"

// GetOAuthToken retrieves the Claude Code OAuth token.
// On macOS it reads from the system Keychain; on Linux it reads from
// ~/.claude/.credentials.json. Returns nil if the token cannot be found.
func GetOAuthToken() *OAuthToken {
	switch runtime.GOOS {
	case "darwin":
		return getOAuthTokenDarwin()
	case "linux":
		return getOAuthTokenLinux()
	default:
		return nil
	}
}

// getOAuthTokenDarwin reads the token from macOS Keychain.
func getOAuthTokenDarwin() *OAuthToken {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainServiceName, "-w").Output()
	if err != nil {
		return nil
	}

	var creds struct {
		ClaudeAiOauth *OAuthToken `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(out, &creds); err != nil {
		return nil
	}

	if creds.ClaudeAiOauth == nil || creds.ClaudeAiOauth.AccessToken == "" {
		return nil
	}

	return creds.ClaudeAiOauth
}

// getOAuthTokenLinux reads the token from ~/.claude/.credentials.json.
func getOAuthTokenLinux() *OAuthToken {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return nil
	}

	var creds struct {
		ClaudeAiOauth *OAuthToken `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil
	}

	if creds.ClaudeAiOauth == nil || creds.ClaudeAiOauth.AccessToken == "" {
		return nil
	}

	return creds.ClaudeAiOauth
}
