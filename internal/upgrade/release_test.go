package upgrade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI stands in for api.github.com and counts how often it was asked.
func fakeAPI(t *testing.T, rel Release) *atomic.Int32 {
	t.Helper()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	}))
	t.Cleanup(srv.Close)

	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })

	return &calls
}

func TestLatestRelease(t *testing.T) {
	fakeAPI(t, Release{
		TagName: "v1.2.3",
		Assets:  []Asset{{Name: "csm-linux-amd64", URL: "https://example.test/bin"}},
	})

	rel, err := LatestRelease(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatalf("LatestRelease() = %v", err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want v1.2.3", rel.TagName)
	}
	if got := rel.AssetURL("csm-linux-amd64"); got != "https://example.test/bin" {
		t.Errorf("AssetURL() = %q", got)
	}
	if got := rel.AssetURL("nope"); got != "" {
		t.Errorf("AssetURL() for a missing asset = %q, want empty", got)
	}
}

func TestLatestReleaseNamesRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	_, err := LatestRelease(context.Background(), "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("LatestRelease() = %v, want an error naming the rate limit", err)
	}
}

// isolateHome points the check cache at a temp dir so a test never reads or
// writes the developer's own ~/.claude-monitor.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CSM_NO_UPDATE_CHECK", "")
	return home
}

func TestNoticeReportsNewerRelease(t *testing.T) {
	home := isolateHome(t)
	calls := fakeAPI(t, Release{TagName: "v9.9.9"})

	got := Notice("v0.6.0")
	if !strings.Contains(got, "v9.9.9") || !strings.Contains(got, "csm -upgrade") {
		t.Errorf("Notice() = %q, want it to name the version and the command", got)
	}

	// The answer is cached, so a second call in the same day costs nothing.
	if got2 := Notice("v0.6.0"); got2 != got {
		t.Errorf("second Notice() = %q, want %q", got2, got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("hit the API %d times, want 1 — the cache is not being used", n)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-monitor", "update-check.json")); err != nil {
		t.Errorf("no cache file written: %v", err)
	}
}

func TestNoticeSilentWhenCurrent(t *testing.T) {
	isolateHome(t)
	fakeAPI(t, Release{TagName: "v0.6.0"})

	if got := Notice("v0.6.0"); got != "" {
		t.Errorf("Notice() = %q, want empty when already on the latest release", got)
	}
}

func TestNoticeRespectsOptOut(t *testing.T) {
	isolateHome(t)
	calls := fakeAPI(t, Release{TagName: "v9.9.9"})
	t.Setenv("CSM_NO_UPDATE_CHECK", "1")

	if got := Notice("v0.6.0"); got != "" {
		t.Errorf("Notice() = %q, want empty with CSM_NO_UPDATE_CHECK set", got)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("hit the API %d times with the check opted out, want 0", n)
	}
}

func TestNoticeSkipsDevBuilds(t *testing.T) {
	isolateHome(t)
	calls := fakeAPI(t, Release{TagName: "v9.9.9"})

	if got := Notice("dev"); got != "" {
		t.Errorf("Notice(\"dev\") = %q, want empty", got)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("hit the API %d times for a dev build, want 0", n)
	}
}

func TestNoticeSurvivesAnUnreachableAPI(t *testing.T) {
	isolateHome(t)

	// A server that refuses to answer stands in for being offline. A
	// dashboard must not report the state of GitHub, so this is silence.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if got := Notice("v0.6.0"); got != "" {
		t.Errorf("Notice() = %q, want empty when the API fails", got)
	}
}

func TestNoticeRefetchesAfterCacheExpires(t *testing.T) {
	home := isolateHome(t)
	calls := fakeAPI(t, Release{TagName: "v9.9.9"})

	// A check from before the TTL must not be reused.
	stale, err := json.Marshal(checkState{CheckedAt: time.Now().Add(-2 * cacheTTL), Latest: "v0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude-monitor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update-check.json"), stale, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := Notice("v0.6.0"); !strings.Contains(got, "v9.9.9") {
		t.Errorf("Notice() = %q, want it to have refetched past the stale cache", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("hit the API %d times, want 1", n)
	}
}
