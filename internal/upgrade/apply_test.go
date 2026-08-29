package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRelease serves a csm-shaped binary plus a checksums.txt, the way a real
// GitHub release does. body is what the "binary" contains.
func fakeRelease(t *testing.T, body string) (*httptest.Server, Release) {
	t.Helper()

	sum := sha256.Sum256([]byte(body))
	name := BinaryName()
	sums := fmt.Sprintf("%s  %s\n%s  csm-someother-arch\n", hex.EncodeToString(sum[:]), name, strings.Repeat("0", 64))

	mux := http.NewServeMux()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("/"+checksumsAsset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, sums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, Release{
		TagName: "v9.9.9",
		HTMLURL: srv.URL,
		Assets: []Asset{
			{Name: name, URL: srv.URL + "/" + name},
			{Name: checksumsAsset, URL: srv.URL + "/" + checksumsAsset},
		},
	}
}

// A shell script passes the smoke test as long as it answers `-v` the way csm
// does, which is all Apply requires of the thing it installs.
func csmLikeScript(version string) string {
	return "#!/bin/sh\necho \"csm version " + version + "\"\n"
}

func TestApplyReplacesBinary(t *testing.T) {
	_, rel := fakeRelease(t, csmLikeScript("v9.9.9"))

	dir := t.TempDir()
	exe := filepath.Join(dir, "csm")
	if err := os.WriteFile(exe, []byte(csmLikeScript("v0.1.0")), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := Apply(context.Background(), rel, exe, "v0.1.0", &out); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "v9.9.9") {
		t.Errorf("binary at %s was not replaced: %q", exe, got)
	}
	if info, err := os.Stat(exe); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: mode %v", info.Mode())
	}
	assertNoLeftovers(t, dir)
}

func TestApplyRejectsTamperedDownload(t *testing.T) {
	srv, rel := fakeRelease(t, csmLikeScript("v9.9.9"))

	// Serve different bytes than the ones checksums.txt was built from, which
	// is what a tampered mirror or a corrupted transfer looks like.
	mux := http.NewServeMux()
	mux.HandleFunc("/"+BinaryName(), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, csmLikeScript("v6.6.6"))
	})
	mux.HandleFunc("/"+checksumsAsset, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/"+checksumsAsset, http.StatusFound)
	})
	tampered := httptest.NewServer(mux)
	defer tampered.Close()
	rel.Assets[0].URL = tampered.URL + "/" + BinaryName()
	rel.Assets[1].URL = tampered.URL + "/" + checksumsAsset

	dir := t.TempDir()
	exe := filepath.Join(dir, "csm")
	original := csmLikeScript("v0.1.0")
	if err := os.WriteFile(exe, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Apply(context.Background(), rel, exe, "v0.1.0", io.Discard)
	if err == nil {
		t.Fatal("Apply() = nil, want a checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("Apply() = %v, want a checksum mismatch error", err)
	}

	// The whole point: a failed upgrade leaves a working csm behind.
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("failed upgrade modified the existing binary: %q", got)
	}
	assertNoLeftovers(t, dir)
}

func TestApplyRejectsBinaryThatDoesNotRun(t *testing.T) {
	// Correct checksum, but not csm: the smoke test is the only thing standing
	// between "the bytes we expected" and "a working binary on PATH".
	_, rel := fakeRelease(t, "#!/bin/sh\necho 'some other program'\n")

	dir := t.TempDir()
	exe := filepath.Join(dir, "csm")
	original := csmLikeScript("v0.1.0")
	if err := os.WriteFile(exe, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Apply(context.Background(), rel, exe, "v0.1.0", io.Discard)
	if err == nil {
		t.Fatal("Apply() = nil, want the smoke test to reject it")
	}
	if got, _ := os.ReadFile(exe); string(got) != original {
		t.Errorf("rejected upgrade modified the existing binary: %q", got)
	}
	assertNoLeftovers(t, dir)
}

func TestApplyRequiresChecksumsAsset(t *testing.T) {
	_, rel := fakeRelease(t, csmLikeScript("v9.9.9"))
	rel.Assets = rel.Assets[:1] // drop checksums.txt

	err := Apply(context.Background(), rel, filepath.Join(t.TempDir(), "csm"), "v0.1.0", io.Discard)
	if err == nil || !strings.Contains(err.Error(), checksumsAsset) {
		t.Errorf("Apply() = %v, want an error naming %s", err, checksumsAsset)
	}
}

func TestChecksumForMatchesNamesExactly(t *testing.T) {
	sums := "aaa  csm-linux-arm64.deb\nbbb  csm-linux-arm64\nccc  csm-linux-amd64\n"

	got, err := checksumFor(sums, "csm-linux-arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bbb" {
		t.Errorf("checksumFor() = %q, want %q (matched a .deb line by prefix?)", got, "bbb")
	}

	if _, err := checksumFor(sums, "csm-darwin-arm64"); err == nil {
		t.Error("checksumFor() = nil error for a name that is not listed")
	}
}

// assertNoLeftovers checks that Apply cleaned up after itself: a stray
// .csm-upgrade-* file in the install directory is confusing at best and gets
// mistaken for a partial install at worst.
func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".csm-upgrade-") {
			t.Errorf("Apply() left a temporary file behind: %s", e.Name())
		}
	}
}
