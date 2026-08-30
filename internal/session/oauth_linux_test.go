//go:build linux

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Credentials csm cannot read have to say why. Reported as "no token found",
// a missing file sends the user to sign in again when the real answer might be
// that the file is somewhere else or unreadable.
func TestGetOAuthTokenSaysWhyTheCredentialsAreUnavailable(t *testing.T) {
	t.Run("credentials are there", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		creds := filepath.Join(home, ".claude", ".credentials.json")
		if err := os.WriteFile(creds, []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat-abc"}}`), 0o600); err != nil {
			t.Fatal(err)
		}

		token, err := GetOAuthToken()
		if err != nil {
			t.Fatalf("GetOAuthToken: %v", err)
		}
		if token.AccessToken != "sk-ant-oat-abc" {
			t.Errorf("AccessToken = %q", token.AccessToken)
		}
	})

	// Having no credentials file is the normal state for someone who never
	// signed in, or who uses an API key. Reported as an errno with their home
	// path in it, it reads like a broken install.
	t.Run("never signed in", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		token, err := GetOAuthToken()
		if err == nil {
			t.Fatalf("a missing credentials file returned token %+v and no error", token)
		}
		if strings.Contains(err.Error(), "no such file") {
			t.Errorf("the normal not-signed-in state is reported as an errno: %v", err)
		}
	})

	// A file that is there and cannot be read is a real fault, and the user
	// needs the path to act on it.
	t.Run("credentials cannot be read", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		// A directory where the file belongs fails the read for every user,
		// including root, so the test does not depend on who runs it.
		if err := os.MkdirAll(filepath.Join(home, ".claude", ".credentials.json"), 0o700); err != nil {
			t.Fatal(err)
		}

		token, err := GetOAuthToken()
		if err == nil {
			t.Fatalf("an unreadable credentials file returned token %+v and no error", token)
		}
		if !strings.Contains(err.Error(), ".credentials.json") {
			t.Errorf("the error does not name the file it could not read: %v", err)
		}
	})
}

// The quota panel is where the reason reaches the user. Carrying a generic
// string instead of the real one is what made "OAuth token not found" the
// answer to every failure, including ones no sign-in would fix.
func TestFetchAPIQuotaCarriesTheCredentialFailureToThePanel(t *testing.T) {
	// No credentials, so this returns before any HTTP call.
	t.Setenv("HOME", t.TempDir())

	quota := fetchAPIQuotaUncached()
	if quota.Available {
		t.Fatal("quota reported available with no credentials")
	}
	if quota.Error == "" {
		t.Fatal("the panel is given no reason at all")
	}
	if !strings.Contains(quota.Error, "not signed in") {
		t.Errorf("the panel does not carry the reason GetOAuthToken gave: %q", quota.Error)
	}
}
