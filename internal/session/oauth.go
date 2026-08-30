package session

import (
	"encoding/json"
	"errors"
	"fmt"
)

// OAuthToken holds the Claude Code OAuth credentials.
type OAuthToken struct {
	AccessToken string `json:"accessToken"`
}

// Claude Code keeps its OAuth credentials in a different place on each
// platform: the macOS Keychain, or ~/.claude/.credentials.json on Linux. Each
// platform file declares GetOAuthToken, and this file holds what they share.
//
// GetOAuthToken reports an error rather than a nil token, so a platform csm
// cannot read at all does not reach the user as "no token found". The two need
// different answers: one is a machine to sign in on, the other is a machine to
// stop asking. The token is non-nil whenever the error is nil.

// parseCredentials reads the token out of Claude Code's credentials JSON, which
// both platforms store in the same shape.
//
// An empty access token is an error, not a token. Passed on, it becomes an
// Authorization header with nothing after "Bearer", and the quota panel then
// reports the API's 401 instead of the sign-in that would fix it.
func parseCredentials(data []byte) (*OAuthToken, error) {
	var creds struct {
		ClaudeAiOauth *OAuthToken `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("credentials are not valid JSON: %w", err)
	}
	if creds.ClaudeAiOauth == nil || creds.ClaudeAiOauth.AccessToken == "" {
		return nil, errors.New("credentials hold no access token")
	}
	return creds.ClaudeAiOauth, nil
}
