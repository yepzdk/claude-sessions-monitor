package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"
)

// printf writes user-facing progress. The error is dropped deliberately and in
// one place: if the stream csm is reporting to has gone away, there is nowhere
// left to report that it has gone away.
func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// Run implements `csm -upgrade` and `csm upgrade`. It returns the process exit
// code. assumeYes is -y: the answer to the confirmation, given in advance.
//
// Whether stdin can answer a prompt is decided here, once, rather than at the
// point the question is asked: it is the only fact in this package that needs
// a real terminal, and everything downstream is a plain bool.
func Run(version string, out io.Writer, assumeYes bool) int {
	exe, err := resolveExecutable()
	if err != nil {
		printf(out, "Could not locate the running csm binary: %v\n", err)
		return 1
	}
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	return runFor(version, exe, out, newPrompt(os.Stdin, interactive, assumeYes))
}

// runFor is Run with the binary's location supplied rather than discovered.
//
// The split exists so the property this whole feature rests on -- a managed
// install is told what to run and nothing on disk is touched -- is a test
// rather than something a reviewer has to take on trust. Run itself is then
// only the os.Executable call.
func runFor(version, exe string, out io.Writer, ask confirmer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rel, err := LatestRelease(ctx, version)
	if err != nil {
		printf(out, "Could not check for updates: %v\n", err)
		return 1
	}

	// A version we cannot parse is a build from a checkout. The user asked to
	// upgrade, so do it, but say what is being replaced -- silently swapping a
	// developer's own build for a release would be a surprise.
	dev := false
	if _, ok := parseVersion(version); !ok {
		dev = true
		printf(out, "This is a development build (%s); the latest release is %s.\n", version, rel.TagName)
	} else if !IsNewer(version, rel.TagName) {
		printf(out, "csm %s is already the latest release.\n", version)
		return 0
	}

	if method := Detect(exe, nil); method.Managed() {
		printf(out, "csm %s is available (you have %s).\n\n", rel.TagName, version)
		printf(out, "This csm was installed with %s, so upgrading it is that tool's job:\n\n  %s\n\n",
			method.Name(), method.Command())
		printf(out, "Replacing the file directly would leave %s's records wrong, so csm won't.\n", method.Name())
		return 0
	}

	// The replacement is irreversible and one keystroke away, so name the file
	// that is about to be overwritten and ask. Neither branch above reaches
	// here: nothing is touched when another tool owns the binary or when there
	// is nothing newer, and asking there would only train a reflexive `y`.
	if !dev {
		printf(out, "csm %s is available (you have %s).\n", rel.TagName, version)
	}
	printf(out, "This will replace %s.\n", exe)
	if !ask(out, "Continue?") {
		return 0
	}

	if err := Apply(ctx, rel, exe, version, out); err != nil {
		printf(out, "Upgrade failed: %v\n", err)
		return 1
	}
	return 0
}

// resolveExecutable returns the real path of the running binary, following
// symlinks. The symlink matters: ~/.local/bin/csm pointing into a Homebrew
// Cellar is a managed install wearing an unmanaged path, and overwriting the
// link target is exactly what must not happen.
func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	// An unresolvable path is still better than none: the caller uses it to
	// classify the install and to rename over, and both work on the
	// unresolved path. Failing here would block upgrades on systems where
	// /proc is restricted, which is worse than a slightly less precise answer.
	return exe, nil
}

// cacheTTL is how long a background check's answer is reused. Long enough that
// restarting csm all day costs one request, short enough that a release is
// noticed the next day.
const cacheTTL = 24 * time.Hour

// checkState is what gets cached between runs.
type checkState struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// Notice returns a one-line "there is a newer csm" message, or "" when there
// is nothing to say. It performs at most one network request per cacheTTL and
// treats every failure as "nothing to say": a dashboard must not report the
// state of GitHub.
//
// Callers run this off the render path -- it blocks on the network.
func Notice(version string) string {
	// Opt-out for anyone who does not want csm talking to github.com. Any
	// non-empty value counts, NO_COLOR-style: a privacy switch that quietly
	// ignores CSM_NO_UPDATE_CHECK=true because it only recognises "1" fails in
	// the direction that matters, so this errs the other way -- including for
	// "0", which the README says out loud.
	if os.Getenv("CSM_NO_UPDATE_CHECK") != "" {
		return ""
	}
	// Development builds have no release to be behind.
	if _, ok := parseVersion(version); !ok {
		return ""
	}

	latest := cachedLatest()
	if latest == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		rel, err := LatestRelease(ctx, version)
		if err != nil {
			return ""
		}
		latest = rel.TagName
		writeCache(checkState{CheckedAt: time.Now(), Latest: latest})
	}

	if !IsNewer(version, latest) {
		return ""
	}
	return fmt.Sprintf("csm %s is available (you have %s) — run: csm -upgrade", latest, version)
}

// cachePath is where the last check is remembered. It sits beside the origin
// cache in ~/.claude-monitor rather than in a config directory: it is derived
// state, disposable at any time.
func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude-monitor", "update-check.json"), nil
}

// cachedLatest returns the remembered tag while it is still fresh, else "".
func cachedLatest() string {
	path, err := cachePath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var st checkState
	if err := json.Unmarshal(data, &st); err != nil {
		return ""
	}
	if time.Since(st.CheckedAt) > cacheTTL {
		return ""
	}
	return st.Latest
}

// writeCache records a check. Failures are ignored: not being able to cache is
// a reason to check again next time, not a reason to bother the user.
func writeCache(st checkState) {
	path, err := cachePath()
	if err != nil {
		return
	}
	// 0o700 to match the origin store, which shares this directory: it records
	// what this user is running and has no reason to be world-readable.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	// Write-then-rename so two csm instances starting together cannot leave a
	// half-written file that the next run fails to parse.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-check-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), path)
}
