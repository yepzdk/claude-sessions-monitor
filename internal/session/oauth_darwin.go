//go:build darwin

package session

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

// keychainServiceName is the service name Claude Code uses in the macOS Keychain.
const keychainServiceName = "Claude Code-credentials"

// GetOAuthToken reads the token from the macOS Keychain.
func GetOAuthToken() (*OAuthToken, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainServiceName, "-w").Output()
	if err != nil {
		// Output() puts the tool's stderr in ExitError and leaves the error
		// itself as "exit status 44", which tells the user nothing. The
		// Keychain's own wording is the only useful part.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if reason := bytes.TrimSpace(exitErr.Stderr); len(reason) > 0 {
				return nil, fmt.Errorf("reading the macOS Keychain: %s", reason)
			}
		}
		return nil, fmt.Errorf("reading the macOS Keychain: %w", err)
	}
	return parseCredentials(out)
}
