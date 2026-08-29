package session

import (
	"errors"
	"testing"
	"time"
)

// The command lines below are real rows from `ps ax -o args=` on a machine with
// omp installed. Every one of them contains the substring "omp" while belonging
// to something that is not a coding agent, which is why classifyProcess matches
// argv token basenames instead. --kill-ghosts sends SIGTERM to what this
// function identifies, so a false positive here kills a browser or a REPL.
func TestClassifyProcessRejectsPathLookalikes(t *testing.T) {
	notAgents := []struct {
		name string
		argv string
	}{
		{
			name: "puppeteer chrome under ~/.omp",
			argv: "/Users/dev/.omp/puppeteer/chrome/mac_arm-150.0.7871.24/chrome-mac-arm64/" +
				"Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing " +
				"--disable-background-networking --user-data-dir=/Users/dev/.omp/run",
		},
		{
			name: "python runner in omp-python-runner temp dir",
			argv: "/opt/homebrew/Cellar/python@3.14/3.14.7/Frameworks/Python.framework/Versions/" +
				"3.14/Resources/Python.app/Contents/MacOS/Python -u " +
				"/var/folders/bb/y9rk0d2x1fq/T/omp-python-runner/runner-c6z28p.py",
		},
		{
			name: "path argument that ends in omp",
			argv: "cat /etc/omp",
		},
		{
			name: "bare interpreter",
			argv: "bun",
		},
		{
			name: "claude desktop helper, not the CLI",
			argv: "/Applications/Claude.app/Contents/Frameworks/Claude Helper (Renderer).app/" +
				"Contents/MacOS/Claude Helper (Renderer) --type=renderer",
		},
		{
			name: "empty",
			argv: "",
		},
	}

	for _, tc := range notAgents {
		if got := classifyProcess(tc.argv); got != HarnessNone {
			t.Errorf("%s: classifyProcess = %q, want HarnessNone; "+
				"--kill-ghosts would be allowed to SIGTERM this process", tc.name, got)
		}
	}
}

func TestClassifyProcessIdentifiesAgents(t *testing.T) {
	agents := []struct {
		name string
		argv string
		want Harness
	}{
		// How omp actually runs: the runtime is argv[0] and the only evidence
		// of which program this is sits in argv[1].
		{"omp through bun", "bun /Users/dev/.bun/bin/omp", HarnessOMP},
		{"omp through bun with runtime flags", "bun --smol /Users/dev/.bun/bin/omp", HarnessOMP},
		{"omp as its own binary", "/opt/homebrew/bin/omp --resume", HarnessOMP},
		{"omp with arguments", "bun /Users/dev/.bun/bin/omp -c", HarnessOMP},
		{"claude cli", "/Users/dev/.local/bin/claude --resume", HarnessClaude},
		{"claude cli without arguments", "claude", HarnessClaude},
	}

	for _, tc := range agents {
		if got := classifyProcess(tc.argv); got != tc.want {
			t.Errorf("%s: classifyProcess = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The recheck runs immediately before SIGTERM. Requiring the *session's* harness
// rather than "any coding agent" is what stops a reissued pid that now belongs to
// the other agent from being killed on the first one's behalf.
func TestIsHarnessProcessRequiresMatchingHarness(t *testing.T) {
	restore := processArgs
	defer func() { processArgs = restore }()
	processArgs = func(int) ([]byte, error) {
		return []byte("bun /Users/dev/.bun/bin/omp\n"), nil
	}

	if !isHarnessProcess(1, HarnessOMP) {
		t.Error("an omp process was not recognised for an omp session")
	}
	if isHarnessProcess(1, HarnessClaude) {
		t.Error("an omp process passed the recheck for a Claude session")
	}
	if isHarnessProcess(1, HarnessNone) {
		t.Error("an unattributed session passed the recheck; it can never name a process to kill")
	}
}

// A pid whose process has exited leaves ps with nothing to print, and the
// caller must read that as "do not signal" rather than "unchanged".
func TestIsHarnessProcessRejectsUnreadablePID(t *testing.T) {
	restore := processArgs
	defer func() { processArgs = restore }()
	processArgs = func(int) ([]byte, error) {
		return nil, errors.New("ps: no such process")
	}

	if isHarnessProcess(1, HarnessClaude) {
		t.Error("a pid ps could not read passed the pre-SIGTERM recheck")
	}
}

// Ghosts are killed by pid, and the harness travels with the pid so the recheck
// has something to verify against. Dropping it here would leave every ghost
// unattributed, and an unattributed ghost is never killed.
func TestGhostsFromCarriesHarness(t *testing.T) {
	sessions := []Session{
		{Project: "work/api", GhostPID: 4100, Harness: HarnessOMP,
			LastActivity: time.Now().Add(-3 * time.Hour), PIDConfident: true, IsGhost: true},
	}

	ghosts := ghostsFrom(sessions)

	if len(ghosts) != 1 {
		t.Fatalf("got %d ghosts, want 1", len(ghosts))
	}
	if ghosts[0].Harness != HarnessOMP {
		t.Errorf("Harness = %q, want %q", ghosts[0].Harness, HarnessOMP)
	}
}
