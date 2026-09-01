package session

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Credentials that parse but carry no access token used to count as a token.
// csm then sent "Bearer" with nothing after it, and the quota panel reported
// the API's rejection instead of the sign-in that would fix it.
func TestParseCredentialsRejectsCredentialsWithNoUsableToken(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		token string // empty means the parse must fail
	}{
		{"a real token", `{"claudeAiOauth":{"accessToken":"sk-ant-oat-abc"}}`, "sk-ant-oat-abc"},
		{"the access token is empty", `{"claudeAiOauth":{"accessToken":""}}`, ""},
		{"no oauth section at all", `{"somethingElse":{"accessToken":"abc"}}`, ""},
		{"not JSON", `not json`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := parseCredentials([]byte(tt.data))
			if tt.token == "" {
				if err == nil {
					t.Fatalf("accepted %s as a token", tt.name)
				}
				if token != nil {
					t.Errorf("returned a token alongside an error: %+v", token)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCredentials: %v", err)
			}
			if token.AccessToken != tt.token {
				t.Errorf("AccessToken = %q, want %q", token.AccessToken, tt.token)
			}
		})
	}
}

// Claude Code's credentials carry expiresAt in milliseconds. Read as seconds it
// lands tens of thousands of years out, so every token looks valid and an
// expired one is only discovered by spending a request on a 401.
func TestOAuthTokenExpiry(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		expiresAt int64
		expired   bool
	}{
		{"an hour from now", now.Add(time.Hour).UnixMilli(), false},
		{"an hour ago", now.Add(-time.Hour).UnixMilli(), true},
		{"exactly now", now.UnixMilli(), true},
		{"no deadline stated", 0, false},
		{"a negative deadline", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No access token: expired() reads only the deadline.
			token := &OAuthToken{ExpiresAt: tt.expiresAt}
			if got := token.expired(now); got != tt.expired {
				t.Errorf("expired() = %v, want %v", got, tt.expired)
			}
		})
	}
}

// parseCredentials must label what it read. An unlabelled credential leaves the
// panel unable to say whose numbers it is showing once there is a second source.
func TestParseCredentialsNamesClaudeCodeAsTheSource(t *testing.T) {
	token, err := parseCredentials([]byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat-abc","expiresAt":1788102736516}}`))
	if err != nil {
		t.Fatalf("parseCredentials: %v", err)
	}
	if token.Source != sourceClaudeCode {
		t.Errorf("Source = %q, want %q", token.Source, sourceClaudeCode)
	}
	if token.ExpiresAt != 1788102736516 {
		t.Errorf("ExpiresAt = %d, want the value from the credentials", token.ExpiresAt)
	}
}

// The precedence rule is the whole point of having two sources: Claude Code
// while its token is good, omp when it is not. Getting this wrong is invisible
// -- the panel fills in either way -- until the preferred token is the stale one
// and the view reports a 401 with a working credential sitting unused.
func TestResolveCredentialPrefersTheTokenThatIsStillValid(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	valid := now.Add(time.Hour).UnixMilli()
	lapsed := now.Add(-time.Hour).UnixMilli()

	claudeCode := func(expiresAt int64) func() (*OAuthToken, error) {
		return func() (*OAuthToken, error) {
			return &OAuthToken{AccessToken: "cc", ExpiresAt: expiresAt, Source: sourceClaudeCode}, nil
		}
	}
	omp := func() (*OAuthToken, error) {
		return &OAuthToken{AccessToken: "omp", Source: "omp (person@example.com)"}, nil
	}

	tests := []struct {
		name       string
		claude     func() (*OAuthToken, error)
		omp        func() (*OAuthToken, error)
		wantToken  string
		wantReason string
		wantInMsg  string
	}{
		{
			name:      "a valid Claude Code token wins",
			claude:    claudeCode(valid),
			omp:       omp,
			wantToken: "cc",
		},
		{
			name:      "an expired Claude Code token falls through to omp",
			claude:    claudeCode(lapsed),
			omp:       omp,
			wantToken: "omp",
		},
		{
			name:      "no Claude Code credential falls through to omp",
			claude:    func() (*OAuthToken, error) { return nil, errors.New("not signed in to Claude Code") },
			omp:       omp,
			wantToken: "omp",
		},
		{
			name:       "expired everywhere reports the expiry, not a missing sign-in",
			claude:     claudeCode(lapsed),
			omp:        func() (*OAuthToken, error) { return nil, errNoOMP },
			wantReason: reasonExpired,
			wantInMsg:  "expired at",
		},
		{
			name:       "nothing anywhere reports Claude Code's own reason",
			claude:     func() (*OAuthToken, error) { return nil, errors.New("not signed in to Claude Code") },
			omp:        func() (*OAuthToken, error) { return nil, errNoOMP },
			wantReason: reasonNoCredentials,
			wantInMsg:  "not signed in",
		},
		{
			name:       "an omp that is installed but cannot help is said so",
			claude:     claudeCode(lapsed),
			omp:        func() (*OAuthToken, error) { return nil, errors.New("No active credential found") },
			wantReason: reasonExpired,
			wantInMsg:  "omp could not supply one either",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreClaude, restoreOMP := getClaudeCodeToken, getOMPToken
			getClaudeCodeToken, getOMPToken = tt.claude, tt.omp
			t.Cleanup(func() { getClaudeCodeToken, getOMPToken = restoreClaude, restoreOMP })

			token, credErr := resolveCredential(now)

			if tt.wantToken != "" {
				if credErr != nil {
					t.Fatalf("no credential resolved: %v", credErr)
				}
				if token.AccessToken != tt.wantToken {
					t.Errorf("used the %q token, want %q", token.AccessToken, tt.wantToken)
				}
				if token.Source == "" {
					t.Error("the resolved credential does not say where it came from")
				}
				return
			}

			if credErr == nil {
				t.Fatalf("resolved a credential when none was usable: %+v", token)
			}
			if credErr.reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", credErr.reason, tt.wantReason)
			}
			if !strings.Contains(credErr.Error(), tt.wantInMsg) {
				t.Errorf("message %q does not carry %q", credErr.Error(), tt.wantInMsg)
			}
		})
	}
}

// An omp that is merely absent must not be mentioned. Naming a tool the user
// never installed as a second failure reads as a broken install.
func TestResolveCredentialDoesNotMentionAnOMPThatIsNotInstalled(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	restoreClaude, restoreOMP := getClaudeCodeToken, getOMPToken
	getClaudeCodeToken = func() (*OAuthToken, error) {
		return &OAuthToken{AccessToken: "cc", ExpiresAt: now.Add(-time.Hour).UnixMilli()}, nil
	}
	getOMPToken = func() (*OAuthToken, error) { return nil, errNoOMP }
	t.Cleanup(func() { getClaudeCodeToken, getOMPToken = restoreClaude, restoreOMP })

	_, credErr := resolveCredential(now)
	if credErr == nil {
		t.Fatal("an expired token with no fallback resolved anyway")
	}
	if strings.Contains(credErr.Error(), "omp") {
		t.Errorf("message names omp on a machine without it: %q", credErr.Error())
	}
}
