// Package upgrade updates csm in place, and tells the user when a newer
// release exists.
//
// Two rules shape everything here:
//
//  1. A binary owned by a package manager is never overwritten. Replacing a
//     file that dpkg, rpm, pacman, Homebrew or mise believes it owns leaves
//     that manager's database lying, and the next `apt upgrade` silently
//     reverts the user. Those installs get the right command printed instead.
//  2. Nothing is installed unverified. Every download is checked against the
//     release's checksums.txt before it is allowed near the target path.
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const (
	// repo is the source of releases. Hardcoded on purpose: an upgrade that
	// can be pointed at an arbitrary host by configuration is a way to install
	// someone else's binary over csm.
	repo = "yepzdk/claude-sessions-monitor"

	// apiBase is overridden only by tests (httptest server).
	defaultAPIBase = "https://api.github.com"

	// checksumsAsset must match the name the release workflow uploads.
	checksumsAsset = "checksums.txt"
)

// Release is the subset of a GitHub release csm needs.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// AssetURL returns the download URL for the named asset, or "" if the release
// does not carry it.
func (r Release) AssetURL(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// BinaryName is the release asset holding the csm binary for this machine.
func BinaryName() string {
	return fmt.Sprintf("csm-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// userAgent identifies csm to the GitHub API. Same reasoning as the Anthropic
// client in internal/session: be greppable, imitate nothing.
func userAgent(version string) string {
	return "csm/" + strings.TrimPrefix(version, "v") + " (+https://github.com/" + repo + ")"
}

// client is the HTTP client for every request in this package. The timeout is
// generous enough for a ~10MB binary on a slow link but still bounded, so a
// hung mirror cannot wedge `csm -upgrade` forever.
var client = &http.Client{Timeout: 2 * time.Minute}

// apiBase is a variable so tests can redirect it; production never changes it.
var apiBase = defaultAPIBase

// LatestRelease fetches the newest published release from the GitHub API.
func LatestRelease(ctx context.Context, version string) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", userAgent(version))
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// 403 here is nearly always the unauthenticated rate limit (60/hour
		// per IP), which is worth naming: it is transient and not the user's
		// fault, and reads as a mystery otherwise.
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return Release{}, fmt.Errorf("GitHub rate limit reached — try again later")
		}
		return Release{}, fmt.Errorf("GitHub returned %s", resp.Status)
	}

	// Cap the body: the releases endpoint answers in kilobytes, and an
	// unbounded decode of a redirected or hostile response is free memory for
	// whoever controls it.
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return Release{}, fmt.Errorf("could not read GitHub's response: %w", err)
	}
	if rel.TagName == "" {
		return Release{}, fmt.Errorf("GitHub returned a release with no tag")
	}
	return rel, nil
}
