package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// errNoOMP reports that this machine has no omp at all, which is a different
// answer from omp having no Anthropic credential: one is a tool that was never
// installed, the other a tool to sign in to. Only the second is worth
// mentioning to someone whose Claude Code token has expired.
var errNoOMP = errors.New("omp is not installed")

// ompCommandTimeout bounds each omp invocation. It matches the usage API's own
// client timeout: a quota panel that hangs is worse than one that says why it
// is empty. Measured cost of the two calls is well under a second. A variable
// so the test for the hang guard does not have to wait out the real value.
var ompCommandTimeout = 5 * time.Second

// ompSystemPaths are the machine-wide places omp installs to. A variable so a
// test can exclude a real omp on the machine running it -- an absolute path is
// not redirectable the way the home-relative ones are.
var ompSystemPaths = []string{"/opt/homebrew/bin/omp", "/usr/local/bin/omp"}

// ompAccountLine matches a row of `omp token anthropic --list`, which numbers
// each account and follows it with the email, as in
// "1. person@example.com (Org Name)".
var ompAccountLine = regexp.MustCompile(`^\s*(\d+)\.\s+(\S+)`)

// ompAccount is one of omp's stored Anthropic accounts.
type ompAccount struct {
	index int
	email string
}

// ompOAuthToken asks omp for an Anthropic access token.
//
// This goes through omp's own `token` command rather than reading
// ~/.omp/agent/agent.db, which holds the credential in plaintext. The command
// refreshes the token before printing it, which is the whole reason omp can
// answer when Claude Code's stored copy cannot, and going through it keeps csm
// out of another tool's private schema and off the SQLite dependency that would
// end the single static binary. See "What csm touches outside its own process"
// in docs/ARCHITECTURE.md.
func ompOAuthToken() (*OAuthToken, error) {
	binary, err := ompBinary()
	if err != nil {
		return nil, err
	}

	accounts, err := ompAccounts(binary)
	if err != nil {
		return nil, err
	}
	account := ompAccountFor(accounts)

	token, err := runOMP(binary, "token", "anthropic", "--account", strconv.Itoa(account.index))
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, errors.New("omp returned an empty Anthropic token")
	}

	// No ExpiresAt: omp refreshes on read, so what it just printed is current.
	// Inventing a deadline here could only make csm reject a good token.
	return &OAuthToken{
		AccessToken: token,
		Source:      "omp (" + account.email + ")",
	}, nil
}

// ompBinary locates omp.
//
// PATH first, then the places omp installs itself. csm can be started from a
// desktop launcher or a login item, which inherits a PATH that has none of the
// per-user bin directories on it, and "omp is not installed" would then be
// wrong on a machine that plainly has it.
func ompBinary() (string, error) {
	if path, err := exec.LookPath("omp"); err == nil {
		return path, nil
	}

	candidates := ompSystemPaths
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append([]string{
			filepath.Join(home, ".bun", "bin", "omp"),
			filepath.Join(home, ".local", "bin", "omp"),
		}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errNoOMP
}

// ompAccounts lists omp's stored Anthropic accounts.
func ompAccounts(binary string) ([]ompAccount, error) {
	out, err := runOMP(binary, "token", "anthropic", "--list")
	if err != nil {
		return nil, err
	}

	var accounts []ompAccount
	for _, line := range strings.Split(out, "\n") {
		match := ompAccountLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		index, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		accounts = append(accounts, ompAccount{index: index, email: match[2]})
	}
	if len(accounts) == 0 {
		// omp exited successfully but said nothing this parser recognised.
		// Reporting that as "no credential" is honest about the outcome
		// without claiming to know omp's state.
		return nil, errors.New("omp listed no Anthropic account")
	}
	return accounts, nil
}

// ompAccountFor picks which of omp's accounts to ask for a token.
//
// omp round-robins across accounts when none is named, so consecutive fetches
// could report a different account's utilization each time the quota cache
// expires -- numbers that move on their own with nothing to explain them. An
// explicit account pins the choice, and preferring the one Claude Code is
// signed in as keeps the panel about the plan the rest of the view describes.
func ompAccountFor(accounts []ompAccount) ompAccount {
	if len(accounts) == 1 {
		return accounts[0]
	}
	if email := claudeAccountEmail(); email != "" {
		for _, account := range accounts {
			if strings.EqualFold(account.email, email) {
				return account
			}
		}
	}
	return accounts[0]
}

// claudeAccountEmail returns the account Claude Code is signed in as.
//
// Only called to disambiguate between several omp accounts, so the common
// machine -- one account each -- never pays for parsing a file that runs to
// hundreds of kilobytes. Every failure is silent on purpose: the caller has a
// working fallback, and this file is not csm's to depend on.
func claudeAccountEmail() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return ""
	}

	var settings struct {
		OAuthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ""
	}
	return settings.OAuthAccount.EmailAddress
}

// runOMP invokes omp and returns its trimmed stdout.
//
// omp prints the token on stdout and its reasons on stderr, and exits non-zero
// when it has no credential to print. The stderr line is the useful part: it
// distinguishes "no active credential" from a broken install, where the exit
// status alone says neither.
func runOMP(binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ompCommandTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// No stdin. omp must not be able to mistake this for an interactive run and
	// block the dashboard waiting on an answer nobody is there to give.
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("omp %s timed out", strings.Join(args, " "))
		}
		if reason := firstLine(stderr.String()); reason != "" {
			return "", errors.New(reason)
		}
		return "", fmt.Errorf("omp %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// firstLine returns the first non-empty line of s, trimmed. omp's failures run
// to a second line of advice that the one-line quota panel has no room for.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
