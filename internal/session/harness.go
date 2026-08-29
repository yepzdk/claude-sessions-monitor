package session

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// harnessProcess is one running coding-agent process, as the ps scan found it.
type harnessProcess struct {
	pid     int
	harness Harness
	cwd     string // resolved working directory, absolute
	tty     string // controlling terminal, or ps's placeholder when there is none
	orphan  bool   // parent shell or IDE is gone and init adopted it
}

// pidsByDir groups one harness's processes into the buckets its session store
// uses, so a log directory joins to the processes running in it by a plain
// string-key match. key turns a working directory into that bucket name: the
// two harnesses encode it differently, and neither encoding is this function's
// business.
//
// Several processes can share a directory (two sessions in two tabs), so the
// values are lists.
func pidsByDir(procs []harnessProcess, h Harness, key func(cwd string) string) map[string][]int {
	dirs := make(map[string][]int)
	for _, p := range procs {
		if p.harness != h {
			continue
		}
		k := key(p.cwd)
		dirs[k] = append(dirs[k], p.pid)
	}
	return dirs
}

// procsByPID indexes the scan by pid, for the lookups that only make sense once
// a pid has been paired to a specific log: whether it is orphaned, and which
// terminal it is attached to.
func procsByPID(procs []harnessProcess) map[int]harnessProcess {
	byPID := make(map[int]harnessProcess, len(procs))
	for _, p := range procs {
		byPID[p.pid] = p
	}
	return byPID
}

// interpreters are the runtimes a harness can be launched through, where argv[0]
// names the runtime and the program itself is a later argument.
var interpreters = map[string]bool{"bun": true, "node": true, "deno": true}

// classifyProcess decides which harness, if any, a command line belongs to.
//
// It matches the basename of a whole argv token, never a substring of the
// command line, because a harness's name appears in paths that have nothing to
// do with a session. Both of these run on an ordinary machine with omp
// installed:
//
//	~/.omp/puppeteer/chrome/.../Google Chrome for Testing --disable-...
//	.../Python -u /var/folders/.../T/omp-python-runner/runner-xxxx.py
//
// strings.Contains(argv, "omp") claims both. One is a browser, the other a
// Python REPL, and --kill-ghosts would SIGTERM either.
//
// Claude Code ships a binary named claude, so argv[0] identifies it. omp
// normally runs as `bun /Users/<user>/.bun/bin/omp`, where argv[0] is only the
// runtime and the evidence sits in a later argument -- but only for a known
// interpreter, so a path argument that happens to end in /omp (`cat /etc/omp`)
// is not mistaken for the agent itself.
func classifyProcess(argv string) Harness {
	fields := strings.Fields(argv)
	if len(fields) == 0 {
		return HarnessNone
	}

	argv0 := filepath.Base(fields[0])
	if strings.HasSuffix(argv0, "claude") {
		return HarnessClaude
	}
	if argv0 == "omp" {
		return HarnessOMP
	}
	if interpreters[argv0] {
		for _, f := range fields[1:] {
			// Runtime flags precede the script; the first non-flag argument is
			// the program being run and the only one worth believing.
			if strings.HasPrefix(f, "-") {
				continue
			}
			if filepath.Base(f) == "omp" {
				return HarnessOMP
			}
			break
		}
	}
	return HarnessNone
}

// processArgs reads one process's full command line. A var so a test can supply
// an argv without spawning anything.
var processArgs = func(pid int) ([]byte, error) {
	// ps directly, with no shell pipeline, to avoid shell injection risks.
	return exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
}

// isHarnessProcess reports whether pid currently belongs to a process of the
// given harness. It is the last check before SIGTERM: Discover ran earlier, and
// a pid it named can have exited and had its number reissued since.
//
// The harness has to match the session's own. A weaker "is this any coding
// agent" test would accept a recycled pid that now belongs to a different
// harness, which is the same coin flip PIDConfident exists to refuse. An
// unattributed session (HarnessNone) can never pass.
func isHarnessProcess(pid int, h Harness) bool {
	if h == HarnessNone {
		return false
	}
	out, err := processArgs(pid)
	if err != nil {
		return false
	}
	return classifyProcess(strings.TrimSpace(string(out))) == h
}
