package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxBinarySize bounds a download that is otherwise attacker-influenced. csm
// ships at ~10MB; 200MB is far above any plausible growth and far below "fill
// the disk".
const maxBinarySize = 200 << 20

// Apply downloads the release binary for this machine, verifies it against the
// release's checksums.txt, and replaces the binary at exePath.
//
// The replacement is a rename within exePath's own directory, which is atomic:
// a download interrupted halfway leaves the old csm in place rather than a
// truncated file on the user's PATH. It also works while the current csm is
// running, because unlinking a running executable is fine on Unix.
func Apply(ctx context.Context, rel Release, exePath, version string, out io.Writer) error {
	name := BinaryName()
	binURL := rel.AssetURL(name)
	if binURL == "" {
		return fmt.Errorf("release %s has no %s asset", rel.TagName, name)
	}
	sumsURL := rel.AssetURL(checksumsAsset)
	if sumsURL == "" {
		return fmt.Errorf("release %s has no %s — upgrade manually from %s",
			rel.TagName, checksumsAsset, rel.HTMLURL)
	}

	printf(out, "Downloading %s %s...\n", name, rel.TagName)

	sums, err := fetch(ctx, sumsURL, version, 1<<20)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", checksumsAsset, err)
	}
	want, err := checksumFor(string(sums), name)
	if err != nil {
		return err
	}

	dir := filepath.Dir(exePath)
	// The temp file lives beside the target, not in /tmp: os.Rename cannot
	// cross filesystems, and ~/.local/bin and /tmp routinely are on different
	// ones.
	tmp, err := os.CreateTemp(dir, ".csm-upgrade-*")
	if err != nil {
		return fmt.Errorf("could not write to %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Any failure past this point must not leave a stray dotfile behind. The
	// success path renames the file away, making this remove a no-op.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	got, err := download(ctx, binURL, version, tmp)
	if err != nil {
		return fmt.Errorf("could not download %s: %w", name, err)
	}
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, want, got)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("could not finish writing %s: %w", tmpName, err)
	}
	// G302 wants 0600. This file is about to become the csm on the user's
	// PATH, so it has to be executable -- by them and by anything else that
	// runs csm on their behalf.
	if err := os.Chmod(tmpName, 0o755); err != nil { //nolint:gosec // G302
		return fmt.Errorf("could not make %s executable: %w", tmpName, err)
	}

	// Run the new binary before trusting it with the user's PATH. A correct
	// checksum only proves we got the bytes the release published -- it says
	// nothing about whether they run on this machine (wrong libc, wrong arch
	// via a mislabeled asset). Finding that out now costs a fork; finding it
	// out later costs the user a working csm.
	if err := smokeTest(ctx, tmpName); err != nil {
		return err
	}

	if err := os.Rename(tmpName, exePath); err != nil {
		return fmt.Errorf("could not replace %s: %w\nIf it is owned by root, re-run with sudo", exePath, err)
	}

	printf(out, "Upgraded to csm %s.\n", rel.TagName)
	return nil
}

// smokeTest checks that the downloaded binary actually runs here.
func smokeTest(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "-v").Output()
	if err != nil {
		return fmt.Errorf("the downloaded binary does not run on this machine (%w) — nothing was changed", err)
	}
	if !strings.HasPrefix(string(out), "csm version") {
		return fmt.Errorf("the downloaded binary is not csm — nothing was changed")
	}
	return nil
}

// checksumFor pulls one file's expected hash out of a sha256sum-format file.
func checksumFor(sums, name string) (string, error) {
	for line := range strings.SplitSeq(sums, "\n") {
		fields := strings.Fields(line)
		// "<hash>  <name>" -- match the name exactly so csm-linux-arm64 does
		// not pick up the line for csm-linux-arm64.deb.
		if len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s has no entry for %s", checksumsAsset, name)
}

// download streams url into w and returns the hex SHA-256 of what was written.
// Hashing as it streams avoids reading the binary back off disk.
func download(ctx context.Context, url, version string, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent(version))

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned %s", resp.Status)
	}

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, maxBinarySize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetch reads a small URL fully into memory.
func fetch(ctx context.Context, url, version string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent(version))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}
