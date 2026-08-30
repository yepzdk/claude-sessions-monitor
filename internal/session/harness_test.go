package session

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// The command lines below are real rows from a machine with omp installed.
// Every one of them contains the substring "omp" while belonging to something
// that is not a coding agent, which is why classifyProcess matches argv element
// basenames instead. --kill-ghosts sends SIGTERM to what this function
// identifies, so a false positive here kills a browser or a REPL.
func TestClassifyProcessRejectsPathLookalikes(t *testing.T) {
	notAgents := []struct {
		name string
		argv []string
	}{
		{
			name: "puppeteer chrome under ~/.omp",
			argv: []string{
				"/Users/dev/.omp/puppeteer/chrome/mac_arm-150.0.7871.24/chrome-mac-arm64/" +
					"Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
				"--disable-background-networking",
				"--user-data-dir=/Users/dev/.omp/run",
			},
		},
		{
			name: "python runner in omp-python-runner temp dir",
			argv: []string{
				"/opt/homebrew/Cellar/python@3.14/3.14.7/Frameworks/Python.framework/Versions/" +
					"3.14/Resources/Python.app/Contents/MacOS/Python",
				"-u",
				"/var/folders/bb/y9rk0d2x1fq/T/omp-python-runner/runner-c6z28p.py",
			},
		},
		{name: "path argument that ends in omp", argv: []string{"cat", "/etc/omp"}},
		{name: "bare interpreter", argv: []string{"bun"}},
		{
			name: "claude desktop helper, not the CLI",
			argv: []string{
				"/Applications/Claude.app/Contents/Frameworks/Claude Helper (Renderer).app/" +
					"Contents/MacOS/Claude Helper (Renderer)",
				"--type=renderer",
			},
		},
		{name: "no argv at all", argv: nil},
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
		argv []string
		want Harness
	}{
		// How omp actually runs: the runtime is argv[0] and the only evidence
		// of which program this is sits in a later element.
		{"omp through bun", []string{"bun", "/Users/dev/.bun/bin/omp"}, HarnessOMP},
		{"omp through bun with a runtime flag", []string{"bun", "--smol", "/Users/dev/.bun/bin/omp"}, HarnessOMP},
		{"omp as its own binary", []string{"/opt/homebrew/bin/omp", "--resume"}, HarnessOMP},
		{"omp with arguments", []string{"bun", "/Users/dev/.bun/bin/omp", "-c"}, HarnessOMP},
		{"claude cli", []string{"/Users/dev/.local/bin/claude", "--resume"}, HarnessClaude},
		{"claude cli without arguments", []string{"claude"}, HarnessClaude},
	}

	for _, tc := range agents {
		if got := classifyProcess(tc.argv); got != tc.want {
			t.Errorf("%s: classifyProcess = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Reading argv as the kernel stored it, rather than re-splitting a printed
// command line, is what makes a path holding a space safe. Splitting on
// whitespace breaks both ways: the agent is missed, and an unrelated binary
// under a directory named "claude something" is mistaken for it and handed to
// SIGTERM.
func TestClassifyProcessReadsAPathHoldingASpaceAsOneElement(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want Harness
	}{
		{
			name: "claude installed under a path with a space",
			argv: []string{"/Volumes/My Disk/bin/claude", "--resume"},
			want: HarnessClaude,
		},
		{
			name: "an unrelated binary under a directory whose name ends in claude",
			argv: []string{"/Users/dev/Projects/claude tools/bin/server", "--port", "80"},
			want: HarnessNone,
		},
	}

	for _, tc := range tests {
		if got := classifyProcess(tc.argv); got != tc.want {
			t.Errorf("%s: classifyProcess = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The basename has to match exactly. A loose suffix match lets any binary whose
// name happens to end in "claude" pass the guard that runs immediately before
// SIGTERM.
func TestClassifyProcessRejectsNamesThatMerelyEndInAnAgentName(t *testing.T) {
	for _, argv0 := range []string{"notclaude", "wrap-claude", "/home/u/bin/work-claude", "/usr/bin/chomp"} {
		if got := classifyProcess([]string{argv0}); got != HarnessNone {
			t.Errorf("classifyProcess([%q]) = %q, want HarnessNone", argv0, got)
		}
	}
}

// Which element holds the program is not knowable without the runtime's own
// flag table, so every non-flag element is considered and the evidence required
// of one is raised: a path ending in /omp, never the bare word.
func TestClassifyProcessHandlesRuntimeFlagsThatTakeAValue(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want Harness
	}{
		{
			name: "a runtime flag whose value is a path, before the program",
			argv: []string{"bun", "--cwd", "/proj", "/Users/dev/.bun/bin/omp"},
			want: HarnessOMP,
		},
		{
			name: "a module named omp preloaded into an unrelated server",
			argv: []string{"node", "-r", "omp", "/app/server.js"},
			want: HarnessNone,
		},
	}

	for _, tc := range tests {
		if got := classifyProcess(tc.argv); got != tc.want {
			t.Errorf("%s: classifyProcess = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The recheck runs immediately before SIGTERM. Requiring the *session's*
// harness rather than "any coding agent" is what stops a reissued pid that now
// belongs to the other agent from being killed on the first one's behalf.
func TestVerifyGhostProcessRequiresTheSessionsOwnHarness(t *testing.T) {
	swapArgvLookup(t, func(int) ([]string, error) {
		return []string{"bun", "/Users/dev/.bun/bin/omp"}, nil
	})

	if err := verifyGhostProcess(1, HarnessOMP); err != nil {
		t.Errorf("an omp process was not recognised for an omp session: %v", err)
	}
	if err := verifyGhostProcess(1, HarnessClaude); err == nil {
		t.Error("an omp process passed the recheck for a Claude session")
	}
	if err := verifyGhostProcess(1, HarnessNone); err == nil {
		t.Error("an unattributed session passed the recheck; it can never name a process to kill")
	}
}

// A pid whose process is gone and a pid that now belongs to something else are
// different outcomes: the first is what --kill-ghosts wanted, the second is csm
// refusing to signal. Only the second is worth reporting, so they have to be
// distinguishable.
func TestVerifyGhostProcessSeparatesAnExitedPIDFromARecycledOne(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		err      error
		wantGone bool
	}{
		{name: "the process is gone", err: errors.New("no such process"), wantGone: true},
		{name: "the pid has no command line", argv: nil, wantGone: true},
		{name: "the pid now belongs to something else", argv: []string{"/usr/bin/vim"}, wantGone: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			swapArgvLookup(t, func(int) ([]string, error) { return tc.argv, tc.err })

			err := verifyGhostProcess(1, HarnessClaude)
			if err == nil {
				t.Fatal("the recheck passed; SIGTERM would be sent")
			}
			if got := errors.Is(err, errProcessGone); got != tc.wantGone {
				t.Errorf("errors.Is(err, errProcessGone) = %v, want %v (err: %v)", got, tc.wantGone, err)
			}
		})
	}
}

// A ghost csm declines to signal used to be dropped by a bare continue, so
// --kill-ghosts printed "No processes were terminated (they may have already
// exited)" and exited 0 for a process csm had listed one line earlier and
// refused to touch. The user could not tell that from a clean run.
func TestKillGhostsReportsAGhostItRefusesToSignal(t *testing.T) {
	// os.Getpid() is used deliberately: it is a live process, so nothing here
	// is excused by the pid being gone. It is never signalled, because the
	// recheck rejects it first -- which is the whole point of the test.
	self := os.Getpid()
	swapArgvLookup(t, func(int) ([]string, error) { return []string{"/usr/bin/vim"}, nil })

	killed, failed := killGhosts([]GhostProcess{
		{PID: self, Project: "work/api", Harness: HarnessClaude},
	})

	if len(killed) != 0 {
		t.Fatalf("killed %v; the recheck should have refused this pid", killed)
	}
	if len(failed) != 1 {
		t.Fatalf("got %d reported failures, want 1: a refusal to signal must not read as a clean run", len(failed))
	}
	if failed[0].Ghost.PID != self {
		t.Errorf("reported pid %d, want %d", failed[0].Ghost.PID, self)
	}
	// The reason has to name what the pid actually is, or the user cannot tell
	// a recycled pid from a session csm misclassified.
	if got := failed[0].Err.Error(); !strings.Contains(got, "/usr/bin/vim") {
		t.Errorf("reason %q does not say what the pid now belongs to", got)
	}
}

// A ghost that exited between the scan and the signal is the outcome
// --kill-ghosts wanted, and reporting it would make every ordinary run look
// like a partial failure.
func TestKillGhostsStaysSilentAboutAGhostThatAlreadyExited(t *testing.T) {
	swapArgvLookup(t, func(int) ([]string, error) { return nil, errors.New("no such process") })

	killed, failed := killGhosts([]GhostProcess{
		{PID: 999999, Project: "work/api", Harness: HarnessClaude},
	})

	if len(killed) != 0 || len(failed) != 0 {
		t.Errorf("killed=%v failed=%v, want both empty", killed, failed)
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

// comm is the cheap prefilter and is deliberately loose: missing an agent here
// means never reading the argv that would have identified it.
func TestHarnessCandidateAdmitsEveryShapeAnAgentCanRunAs(t *testing.T) {
	for _, comm := range []string{"claude", "omp", "bun", "node", "deno"} {
		if !harnessCandidate(comm) {
			t.Errorf("comm %q is not admitted, so its argv is never read", comm)
		}
	}
	for _, comm := range []string{"bash", "Google Chrome for Testing", "Python"} {
		if harnessCandidate(comm) {
			t.Errorf("comm %q is admitted; the prefilter is wider than it needs to be", comm)
		}
	}
}

// The parsers are per-platform and both are fed by the kernel, which no fixture
// reproduces exactly. This runs the real read against the one process whose
// argv the test already knows: its own.
func TestProcessArgvReadsThisProcessesOwnCommandLine(t *testing.T) {
	argv, err := processArgv(os.Getpid())
	if err != nil {
		t.Fatalf("processArgv(self): %v", err)
	}
	if len(argv) == 0 {
		t.Fatal("processArgv(self) returned no arguments")
	}
	// os.Args is what the runtime received; argv[0] is what the kernel stored.
	if argv[0] != os.Args[0] {
		t.Errorf("argv[0] = %q, want %q", argv[0], os.Args[0])
	}
}
