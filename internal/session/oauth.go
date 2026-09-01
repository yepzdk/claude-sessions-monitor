package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// sourceClaudeCode names Claude Code as the origin of a credential, for the
// quota panel. omp builds its own label, which carries the account email.
const sourceClaudeCode = "Claude Code"

// OAuthToken holds an Anthropic OAuth credential.
type OAuthToken struct {
	AccessToken string `json:"accessToken"`
	// ExpiresAt is the credential's own deadline, in epoch milliseconds. Zero
	// means the credential states no deadline, which is not the same as
	// expired: omp hands over a token it has just refreshed, and older Claude
	// Code credentials had no such field.
	ExpiresAt int64 `json:"expiresAt"`
	// Source names the tool the credential came from. Not part of any on-disk
	// shape -- it exists so the quota panel can say whose numbers it is
	// showing, because a panel that silently switched accounts would report a
	// different plan's utilization with no way to tell.
	Source string `json:"-"`
}

// Claude Code keeps its OAuth credentials in a different place on each
// platform: the macOS Keychain, or ~/.claude/.credentials.json on Linux. Each
// platform file declares GetOAuthToken, and this file holds what they share.
//
// GetOAuthToken reports an error rather than a nil token, so a platform csm
// cannot read at all does not reach the user as "no token found". The two need
// different answers: one is a machine to sign in on, the other is a machine to
// stop asking. The token is non-nil whenever the error is nil.

// Indirection for tests. resolveCredential's precedence rule is the logic worth
// covering, and both real sources are unmockable otherwise: GetOAuthToken talks
// to the macOS Keychain, and ompOAuthToken spawns a binary that may not exist.
var (
	getClaudeCodeToken = GetOAuthToken
	getOMPToken        = ompOAuthToken
)

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
	creds.ClaudeAiOauth.Source = sourceClaudeCode
	return creds.ClaudeAiOauth, nil
}

// expired reports whether the credential's own deadline has passed.
//
// A credential that states no deadline is not expired. Treating zero as long
// past would reject every token omp supplies, which is the one source that is
// current by construction.
func (t *OAuthToken) expired(now time.Time) bool {
	if t.ExpiresAt <= 0 {
		return false
	}
	return !now.Before(time.UnixMilli(t.ExpiresAt))
}

// credentialError carries why no usable credential was found, in both the
// wording the user reads and a stable cause the web dashboard can branch on.
// The dashboard used to match on the wording, which broke silently the first
// time it was reworded.
type credentialError struct {
	reason string
	msg    string
}

func (e *credentialError) Error() string { return e.msg }

// resolveCredential picks the credential to query the usage API with.
//
// Claude Code first, but only while its token is still valid. Claude Code
// refreshes lazily -- on its own next run -- so on a machine where it has not
// been used for a day its stored token is expired, and asking the API with it
// buys a 401 that no amount of retrying fixes. omp holds a credential for the
// same Anthropic account and refreshes it whenever it is read, so it can answer
// for the same plan when Claude Code's copy has aged out.
func resolveCredential(now time.Time) (*OAuthToken, *credentialError) {
	claudeToken, claudeErr := getClaudeCodeToken()
	if claudeErr == nil && !claudeToken.expired(now) {
		return claudeToken, nil
	}

	ompToken, ompErr := getOMPToken()
	if ompErr == nil {
		return ompToken, nil
	}

	if claudeErr != nil {
		// Neither tool can supply one. Claude Code's reason is the actionable
		// one; that omp is also not signed in is not what to lead with.
		return nil, &credentialError{reason: reasonNoCredentials, msg: claudeErr.Error()}
	}

	// Naming the moment it lapsed, rather than just "expired", is what
	// separates a token to refresh from an endpoint that is refusing csm.
	msg := fmt.Sprintf("Claude Code's token expired at %s; run `claude` to refresh",
		time.UnixMilli(claudeToken.ExpiresAt).Local().Format("2006-01-02 15:04"))
	if !errors.Is(ompErr, errNoOMP) {
		msg += " (omp could not supply one either)"
	}
	return nil, &credentialError{reason: reasonExpired, msg: msg}
}
