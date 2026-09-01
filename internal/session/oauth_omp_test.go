package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubOMP puts a fake omp first on PATH and points HOME at a temp directory, so
// nothing in these tests reaches the real omp, the real credential store or the
// real ~/.claude.json. script is the body of a shell script that stands in for
// the whole omp CLI.
//
// The stub directory is prepended rather than replacing PATH: the stub still
// wins the lookup for "omp", but the ordinary utilities a stub script may call
// stay resolvable. Replacing PATH outright left `sleep` unresolvable under
// dash, which found it only because bash-as-sh falls back to a compiled-in
// default path -- so the hang-guard test passed on macOS and failed on Linux.
func stubOMP(t *testing.T, script string) string {
	t.Helper()

	home := t.TempDir()
	bin := t.TempDir()
	path := filepath.Join(bin, "omp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("writing the omp stub: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home
}

// The happy path: one account, and the token comes back labelled with the
// account it belongs to. An unlabelled token would leave the panel unable to say
// whose plan it is reporting.
func TestOMPOAuthTokenReturnsALabelledToken(t *testing.T) {
	stubOMP(t, `
case "$*" in
  *--list*) echo "1. person@example.com (Org Name)" ;;
  *"--account 1"*) echo "sk-ant-oat01-from-omp" ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)

	token, err := ompOAuthToken()
	if err != nil {
		t.Fatalf("ompOAuthToken: %v", err)
	}
	if token.AccessToken != "sk-ant-oat01-from-omp" {
		t.Errorf("AccessToken = %q", token.AccessToken)
	}
	if token.Source != "omp (person@example.com)" {
		t.Errorf("Source = %q, want the account named", token.Source)
	}
	// omp refreshes on read, so what it printed is current. A deadline invented
	// here could only make csm reject a good token.
	if token.ExpiresAt != 0 {
		t.Errorf("ExpiresAt = %d, want no invented deadline", token.ExpiresAt)
	}
}

// omp round-robins across accounts when none is named, which would make the
// panel report a different account's utilization every time the cache expires.
// The account Claude Code is signed in as is the one the rest of the view is
// about, so it is the one to pin to.
func TestOMPOAuthTokenPicksTheAccountClaudeCodeUses(t *testing.T) {
	script := `
case "$*" in
  *--list*)
    echo "1. first@example.com (Org One)"
    echo "2. second@example.com (Org Two)"
    ;;
  *"--account 1"*) echo "token-for-first" ;;
  *"--account 2"*) echo "token-for-second" ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`

	t.Run("matches the signed-in account", func(t *testing.T) {
		home := stubOMP(t, script)
		writeClaudeAccount(t, home, "second@example.com")

		token, err := ompOAuthToken()
		if err != nil {
			t.Fatalf("ompOAuthToken: %v", err)
		}
		if token.AccessToken != "token-for-second" {
			t.Errorf("used %q, want the account Claude Code is signed in as", token.AccessToken)
		}
	})

	t.Run("case does not decide it", func(t *testing.T) {
		home := stubOMP(t, script)
		writeClaudeAccount(t, home, "Second@Example.COM")

		token, err := ompOAuthToken()
		if err != nil {
			t.Fatalf("ompOAuthToken: %v", err)
		}
		if token.AccessToken != "token-for-second" {
			t.Errorf("used %q, want the match to ignore case", token.AccessToken)
		}
	})

	// No match to be had is not a reason to let omp round-robin: a pinned
	// arbitrary account still beats numbers that move on their own.
	t.Run("no signed-in account still pins one", func(t *testing.T) {
		stubOMP(t, script)

		token, err := ompOAuthToken()
		if err != nil {
			t.Fatalf("ompOAuthToken: %v", err)
		}
		if token.AccessToken != "token-for-first" {
			t.Errorf("used %q, want the first account", token.AccessToken)
		}
	})
}

// omp exits non-zero with its reason on stderr. That reason is the useful part:
// the exit status alone cannot tell "not signed in" from a broken install.
func TestOMPOAuthTokenCarriesOMPsOwnReason(t *testing.T) {
	stubOMP(t, `
echo 'No active credential found for provider "anthropic".' >&2
echo '--account/--list select among OAuth accounts; this provider has none stored.' >&2
exit 1
`)

	_, err := ompOAuthToken()
	if err == nil {
		t.Fatal("a failing omp produced a token")
	}
	if !strings.Contains(err.Error(), "No active credential") {
		t.Errorf("error %q does not carry omp's reason", err.Error())
	}
	// The second line is advice for a person at a prompt, and the quota panel
	// is one line.
	if strings.Contains(err.Error(), "--account/--list") {
		t.Errorf("error carries omp's second line too: %q", err.Error())
	}
	if errors.Is(err, errNoOMP) {
		t.Error("an omp that ran and refused is reported as not installed")
	}
}

// A successful exit that lists nothing this parser recognises must not be read
// as success. Returning an empty account list would index out of range.
func TestOMPOAuthTokenRejectsAnUnrecognisedAccountList(t *testing.T) {
	stubOMP(t, `
case "$*" in
  *--list*) echo "nothing that looks like an account row" ;;
  *) echo "should not get here" >&2; exit 1 ;;
esac
`)

	if _, err := ompOAuthToken(); err == nil {
		t.Fatal("an unparseable account list was accepted")
	}
}

// An omp that never returns must not take the dashboard with it. FetchAPIQuota
// holds the quota cache's lock across the call, so a stall here stalls the TUI
// and every HTTP consumer too.
//
// The sleep is backgrounded so that killing the shell cannot also kill it: it
// then survives holding the write end of the stdout pipe, which is what makes
// Wait block past the timeout. A plain foreground `sleep` is not enough --
// shells that exec the last command leave no grandchild, so the test would pass
// without exercising the case it exists for (as it did on macOS while failing on
// Linux).
func TestOMPOAuthTokenGivesUpOnAnOMPThatHangs(t *testing.T) {
	stubOMP(t, "sleep 30 &\nwait\n")

	restore := ompCommandTimeout
	ompCommandTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ompCommandTimeout = restore })

	done := make(chan error, 1)
	go func() {
		_, err := ompOAuthToken()
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hanging omp produced a token")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Errorf("error %q does not say it timed out", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ompOAuthToken did not return: the timeout does not bound it")
	}
}

// "omp is not installed" and "omp has no credential" need different answers:
// one is a tool to sign in to, the other is not worth mentioning at all.
func TestOMPBinaryReportsAnAbsentOMPDistinctly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	restore := ompSystemPaths
	ompSystemPaths = nil
	t.Cleanup(func() { ompSystemPaths = restore })

	if _, err := ompBinary(); !errors.Is(err, errNoOMP) {
		t.Errorf("err = %v, want errNoOMP", err)
	}
}

// omp installs to per-user bin directories that a desktop launcher's PATH does
// not carry, and csm can be started from one.
func TestOMPBinaryFindsAnOMPThatIsNotOnPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	restore := ompSystemPaths
	ompSystemPaths = nil
	t.Cleanup(func() { ompSystemPaths = restore })

	installed := filepath.Join(home, ".bun", "bin")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatalf("creating the install directory: %v", err)
	}
	path := filepath.Join(installed, "omp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writing the omp stub: %v", err)
	}

	found, err := ompBinary()
	if err != nil {
		t.Fatalf("ompBinary: %v", err)
	}
	if found != path {
		t.Errorf("found %q, want %q", found, path)
	}
}

// writeClaudeAccount writes the one field of ~/.claude.json csm reads.
func writeClaudeAccount(t *testing.T, home, email string) {
	t.Helper()

	body := `{"oauthAccount":{"emailAddress":"` + email + `"},"other":"ignored"}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing .claude.json: %v", err)
	}
}
