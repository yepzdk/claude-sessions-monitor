package session

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
)

// Harness names the coding agent a session belongs to. csm watches more than
// one, and they agree on nothing that matters here: where logs live, what an
// entry looks like, which process to look for. Every Session carries its
// harness so nothing downstream has to infer it -- most sharply
// KillGhostProcesses, which sends SIGTERM and must never act on a process it
// cannot positively attribute.
type Harness string

const (
	// HarnessNone is a process csm does not recognise as a coding agent. It is
	// the zero value on purpose: code that acts on a process must require a
	// specific harness, never merely "not none".
	HarnessNone Harness = ""
	// HarnessClaude is Claude Code, logging to ~/.claude/projects.
	HarnessClaude Harness = "claude"
	// HarnessOMP is Oh My Pi, logging to ~/.omp/agent/sessions.
	HarnessOMP Harness = "omp"
)

// String names the harness for a human. The zero value has to read as
// something, because it appears in the reason a ghost was not signalled.
func (h Harness) String() string {
	if h == HarnessNone {
		return "no recognised coding agent"
	}
	return string(h)
}

// interpreters are the runtimes a harness can be launched through, where argv[0]
// names the runtime and the program itself is a later argument.
var interpreters = map[string]bool{"bun": true, "node": true, "deno": true}

// harnessCandidate reports whether a process is worth reading a full argv for.
//
// The process table carries comm -- a basename the kernel truncates to 15 bytes
// on Linux and 16 on macOS -- for every process on the machine, and argv costs
// one read per pid. So comm narrows the field and classifyProcess makes the
// decision. This is deliberately the loose half of the pair: it only has to
// avoid missing an agent, never to be right about one, which is why it matches
// suffixes that classifyProcess goes on to reject. Anything dropped here is
// dropped without ever being looked at.
func harnessCandidate(comm string) bool {
	if strings.HasSuffix(comm, "claude") ||
		strings.HasSuffix(comm, "omp") ||
		interpreters[comm] {
		return true
	}
	// comm is the executable's own name, and an installer is free to name the
	// executable after the version rather than the agent: Claude Code's puts
	// it at ~/.local/share/claude/versions/<version>, so comm reads "2.1.250"
	// and the agent's name appears nowhere in it. A session installed that way
	// is invisible to a name-only prefilter -- reported Inactive while it runs,
	// and never a ghost candidate.
	//
	// A leading digit is what those names have in common and program names do
	// not. It costs one extra argv read per such install, and classifyProcess
	// still has to recognise the argv before anything acts on it.
	//
	// ponytail: an install naming the executable something else again --
	// neither the agent's name nor a version -- is still missed here.
	return len(comm) > 0 && comm[0] >= '0' && comm[0] <= '9'
}

// classifyProcess decides which harness, if any, an argument vector belongs to.
//
// It matches the basename of a whole argv element, never a substring of a
// flattened command line, because a harness's name appears in paths that have
// nothing to do with a session. Both of these run on an ordinary machine with
// omp installed:
//
//	~/.omp/puppeteer/chrome/.../Google Chrome for Testing --disable-...
//	.../Python -u /var/folders/.../T/omp-python-runner/runner-xxxx.py
//
// strings.Contains over a command line claims both. One is a browser, the
// other a Python REPL, and --kill-ghosts would SIGTERM either.
//
// Taking argv as the kernel stored it, rather than re-splitting a printed
// command line on spaces, is what makes the match exact: a harness installed
// under a path holding a space (/Volumes/My Disk/bin/claude) is one element
// here, where splitting on whitespace would read it as /Volumes/My and miss
// the agent -- and, worse, would read /Users/dev/claude tools/bin/server as
// the claude binary and hand its pid to SIGTERM.
//
// Claude Code ships a binary named claude, so argv[0] identifies it. omp
// normally runs as `bun /Users/<user>/.bun/bin/omp`, where argv[0] is only the
// runtime and the evidence sits in a later element.
func classifyProcess(argv []string) Harness {
	if len(argv) == 0 {
		return HarnessNone
	}

	switch filepath.Base(argv[0]) {
	case "claude":
		return HarnessClaude
	case "omp":
		return HarnessOMP
	}

	if !interpreters[filepath.Base(argv[0])] {
		return HarnessNone
	}
	// Which element holds the program is not knowable without the runtime's
	// own flag table: `bun --smol prog` and `bun --cwd /proj prog` put it in
	// different places, and stopping at the first non-flag would miss the
	// second. So every non-flag element is considered, and the evidence
	// required of one is raised instead -- see namesAgentExecutable.
	for _, arg := range argv[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if namesAgentExecutable(arg, "omp") {
			return HarnessOMP
		}
	}
	return HarnessNone
}

// namesAgentExecutable reports whether an argv element names the agent's own
// executable, rather than merely a path that happens to end in its name.
//
// A directory argument is the trap. `bun install /home/u/omp` and
// `bun run --cwd /home/u/src/omp test` both end in /omp, and a checkout named
// after the agent is exactly what someone running that agent is likely to
// have. Skipping `-` prefixed elements does not help, because a flag's value is
// its own element and carries no dash.
//
// What separates the real thing is where it lives: a program launched through
// a runtime is a shim in a bin directory (~/.bun/bin/omp, node_modules/.bin/omp),
// never a project directory. Requiring that errs toward not recognising an
// agent, which costs a ghost that is listed but never killed -- where the
// opposite error is a SIGTERM to something that was never an agent at all.
//
// ponytail: a flag value pointing into a bin directory
// (`node --require /opt/omp/bin/omp app.js`) still matches. Excluding a flag's
// value needs the runtime's own flag table.
func namesAgentExecutable(arg, name string) bool {
	if filepath.Base(arg) != name {
		return false
	}
	// A bare "omp" has Dir "." and is rejected here too: it is a module name,
	// as in `node -r omp server.js`, not a path to the agent.
	switch filepath.Base(filepath.Dir(arg)) {
	case "bin", ".bin":
		return true
	}
	return false
}

// errProcessGone reports a pid that no longer names a live process. It is the
// one recheck failure that needs no explanation: the ghost exited on its own
// between the scan and the signal, which is the outcome --kill-ghosts wanted.
var errProcessGone = errors.New("process is gone")

// verifyGhostProcess reports whether pid still belongs to a process of the
// given harness, and if not, why.
//
// This is the last check before SIGTERM. Discover ran earlier, and a pid it
// named can have exited and had its number reissued since. The harness has to
// match the session's own: a weaker "is this any coding agent" test would
// accept a recycled pid that now belongs to a different harness, which is the
// same coin flip PIDConfident exists to refuse. An unattributed session
// (HarnessNone) can never pass.
//
// It returns an error rather than a bool so the caller can tell a pid that
// exited from one that now belongs to something else. Reporting both as "not
// terminated" would leave a user unable to tell a finished ghost from a
// process csm refused to touch.
func verifyGhostProcess(pid int, want Harness) error {
	if want == HarnessNone {
		return errors.New("the session names no coding agent, so no process can be attributed to it")
	}

	argv, err := processArgvFn(pid)
	if err != nil {
		// Only a pid that is genuinely gone earns the silent path in
		// killGhosts. A permission error, or a buffer that would not parse, is
		// csm failing to look -- and reporting that as an exit reproduces the
		// "listed a ghost, signalled nothing, called it a clean run" this
		// guard exists to end. The SIGTERM below discriminates the same way,
		// on ESRCH and ErrProcessDone rather than on any signal error.
		//
		// ENOENT is a pid whose /proc entry is gone; EINVAL is what
		// kern.procargs2 returns for a pid macOS no longer has.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EINVAL) {
			return fmt.Errorf("%w: %w", errProcessGone, err)
		}
		return fmt.Errorf("could not read the command line for pid %d: %w", pid, err)
	}
	// A live process always has an argv. An empty one is a pid whose process
	// has exited but whose entry has not been reaped, or a kernel thread.
	if len(argv) == 0 {
		return fmt.Errorf("%w: no command line", errProcessGone)
	}

	if got := classifyProcess(argv); got != want {
		return fmt.Errorf("pid now belongs to %s, not to %s: %s",
			got, want, strings.Join(argv, " "))
	}
	return nil
}
