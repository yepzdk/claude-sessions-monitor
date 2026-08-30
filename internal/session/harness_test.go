package session

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
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

// A path ending in /omp is not enough: a directory named after the agent is
// exactly what someone running that agent is likely to have, and a flag's value
// is its own argv element with no leading dash, so skipping flags does not keep
// one out. Every case here would be handed to SIGTERM once omp discovery sets
// HarnessOMP.
func TestClassifyProcessRejectsADirectoryNamedAfterTheAgent(t *testing.T) {
	for _, argv := range [][]string{
		{"bun", "install", "/home/u/omp"},
		{"node", "/usr/local/lib/eslint/bin/eslint.js", "/home/u/projects/omp"},
		{"bun", "run", "--cwd", "/home/u/src/omp", "test"},
		{"node", "--require", "/opt/lib/omp", "/app/server.js"},
	} {
		if got := classifyProcess(argv); got != HarnessNone {
			t.Errorf("classifyProcess(%q) = %q, want HarnessNone", argv, got)
		}
	}
}

// The shape that is the agent: a shim in a bin directory, which is where a
// runtime-launched program lives and a project checkout does not.
func TestClassifyProcessAcceptsAnAgentShimInABinDirectory(t *testing.T) {
	for _, argv := range [][]string{
		{"bun", "/Users/dev/.bun/bin/omp"},
		{"node", "/home/u/proj/node_modules/.bin/omp", "--resume"},
	} {
		if got := classifyProcess(argv); got != HarnessOMP {
			t.Errorf("classifyProcess(%q) = %q, want %q", argv, got, HarnessOMP)
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
		{name: "the proc entry is gone", err: fs.ErrNotExist, wantGone: true},
		{name: "macOS has no such pid", err: syscall.EINVAL, wantGone: true},
		{name: "the pid is gone", err: syscall.ESRCH, wantGone: true},
		{name: "the pid has no command line", argv: nil, wantGone: true},
		{name: "the pid now belongs to something else", argv: []string{"/usr/bin/vim"}, wantGone: false},
		// csm failing to look is not the process having exited. Labelling it
		// as one puts it back on the path that reports nothing.
		{name: "the read was refused", err: syscall.EPERM, wantGone: false},
		{name: "the buffer would not parse", err: errors.New("kern.procargs2 ended after 1 of 3 arguments"), wantGone: false},
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
	swapArgvLookup(t, func(int) ([]string, error) { return nil, fs.ErrNotExist })

	killed, failed := killGhosts([]GhostProcess{
		{PID: 999999, Project: "work/api", Harness: HarnessClaude},
	})

	if len(killed) != 0 || len(failed) != 0 {
		t.Errorf("killed=%v failed=%v, want both empty", killed, failed)
	}
}

// A ghost csm could not inspect is not a ghost that exited. Dropping it
// silently produces the same "Found 1 ghost process(es)" followed by "No
// processes were terminated (they may have already exited)" that this guard
// exists to end -- just from a refused read rather than a disagreeing rule.
func TestKillGhostsReportsAGhostItCouldNotInspect(t *testing.T) {
	swapArgvLookup(t, func(int) ([]string, error) { return nil, syscall.EPERM })

	killed, failed := killGhosts([]GhostProcess{
		{PID: 4100, Project: "work/api", Harness: HarnessClaude},
	})

	if len(killed) != 0 {
		t.Fatalf("killed %v without being able to read its command line", killed)
	}
	if len(failed) != 1 {
		t.Fatalf("got %d reported failures, want 1", len(failed))
	}
	if got := failed[0].Err.Error(); !strings.Contains(got, "4100") {
		t.Errorf("reason %q does not name the pid csm could not inspect", got)
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
	// macOS derives comm from the executable's own name, and Claude Code's
	// native installer names that file after the version
	// (~/.local/share/claude/versions/2.1.250). A prefilter that only knows
	// agent names drops a running session before its argv is ever read.
	for _, comm := range []string{"2.1.250", "2.1.250-beta", "2026.8.14"} {
		if !harnessCandidate(comm) {
			t.Errorf("comm %q is not admitted, so an agent installed under a "+
				"version-named path is invisible to discovery", comm)
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

// The badge exists so a row on a two-agent machine is never ambiguous, and the
// origin column grows six columns to hold it. Both of those have to be decided
// the same way every time csm draws, or the table re-flows as sessions idle.
func TestMixedHarnessesAsksAboutTheMachineNotTheVisibleRows(t *testing.T) {
	now := time.Now()

	// The shape that started this: two omp sessions running, both Claude
	// sessions idle for hours. The live view draws only the omp rows, but the
	// machine is plainly running two agents and the column must not resize.
	oneAgentOnScreen := []Session{
		{Harness: HarnessOMP, Status: StatusWorking, LastActivity: now},
		{Harness: HarnessOMP, Status: StatusWaiting, LastActivity: now.Add(-45 * time.Minute)},
		{Harness: HarnessClaude, Status: StatusInactive, LastActivity: now.Add(-4 * time.Hour)},
		{Harness: HarnessClaude, Status: StatusInactive, LastActivity: now.Add(-3 * 24 * time.Hour)},
	}
	if !MixedHarnesses(oneAgentOnScreen) {
		t.Error("a machine with idle sessions from the other agent reported as single-agent; the badge would blink out as sessions go idle")
	}

	if !MixedHarnesses([]Session{
		{Harness: HarnessClaude, LastActivity: now},
		{Harness: HarnessOMP, LastActivity: now},
	}) {
		t.Error("two agents not reported as mixed; the rows would be ambiguous")
	}

	if MixedHarnesses([]Session{
		{Harness: HarnessClaude, LastActivity: now},
		{Harness: HarnessClaude, LastActivity: now.Add(-time.Hour)},
	}) {
		t.Error("one agent reported as mixed; every row would carry a pointless tag")
	}

	if MixedHarnesses(nil) {
		t.Error("an empty dashboard reported as mixed")
	}
}

// The horizon is what keeps the badge from being permanent: trying the other
// agent once must not tag every row for the life of the machine.
func TestMixedHarnessesForgetsAnAgentPastTheHorizon(t *testing.T) {
	now := time.Now()
	claude := Session{Harness: HarnessClaude, LastActivity: now}

	stale := []Session{claude, {Harness: HarnessOMP, LastActivity: now.Add(-HarnessBadgeHorizon - time.Hour)}}
	if MixedHarnesses(stale) {
		t.Errorf("an agent last seen over %v ago still counts, so the badge never clears", HarnessBadgeHorizon)
	}

	fresh := []Session{claude, {Harness: HarnessOMP, LastActivity: now.Add(-HarnessBadgeHorizon + time.Hour)}}
	if !MixedHarnesses(fresh) {
		t.Errorf("an agent seen inside %v was forgotten, so alternating between agents loses the badge", HarnessBadgeHorizon)
	}
}

// A session csm could not attribute is not a third agent. Counting it would
// tag every row on a single-agent machine that has one unreadable log.
func TestMixedHarnessesIgnoresUnattributedSessions(t *testing.T) {
	now := time.Now()
	if MixedHarnesses([]Session{
		{Harness: HarnessClaude, LastActivity: now},
		{Harness: HarnessNone, LastActivity: now},
	}) {
		t.Error("an unattributed session counted as a second agent")
	}
}
