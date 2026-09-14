package session

import (
	"strings"
	"time"
)

// The rules csm applies on its own account, to Bash commands.
//
// Everything in flags.go re-reports a decision the harness made. These rules
// have no such backing: csm is deciding, from the command line alone, that
// something is worth a second look. That difference sets the bar. A rule earns
// its place only if it fires on something a person would want to know about and
// stays quiet through ordinary work -- measured, not assumed. The set below runs
// at roughly 1.5% of Bash calls on a real corpus of 4,758, with high severity at
// about one call in a thousand.
//
// Two properties keep it honest:
//
//   - Severity is decided by the argument, not the verb. `rm -rf .build` in a
//     project and `rm -rf ~/Documents` are the same command and not remotely
//     the same event, so the target path picks the severity.
//   - A rule reads an argument vector, never the raw string. splitCommand does
//     that work; see shell.go for why the raw string is unusable.

// Rule ids for the command classifier.
const (
	RuleRecursiveDelete     = "recursive-delete"
	RulePrivilegeEscalation = "privilege-escalation"
	RuleForcePush           = "force-push"
	RuleHistoryRewrite      = "history-rewrite"
	RuleProtectedBranchPush = "protected-branch-push"
	RuleHardReset           = "hard-reset"
	RuleWorldWritable       = "world-writable"
	RuleRemoteCodeExec      = "remote-code-exec"
)

// protectedBranches are the branch names a push to is worth noting. They are
// the conventional integration branches; a project with a different name for
// its trunk gets no flag, which is the quiet failure rather than the loud one.
var protectedBranches = map[string]bool{"main": true, "master": true, "develop": true}

// shells are the interpreters a downloaded script can be piped into.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "ksh": true, "dash": true}

// downloaders are the commands that fetch a URL to stdout.
var downloaders = map[string]bool{"curl": true, "wget": true}

// commandFlags classifies one Bash command, returning every rule it trips.
func commandFlags(command string, at time.Time, toolUseID string) []Flag {
	segments := splitCommand(command)
	var flags []Flag

	add := func(rule string, sev Severity) {
		flags = append(flags, Flag{
			Rule:      rule,
			Severity:  sev,
			At:        at,
			Tool:      "Bash",
			Detail:    excerpt(command),
			ToolUseID: toolUseID,
		})
	}

	for i, seg := range segments {
		name, args := seg.Command()
		if name == "" {
			continue
		}
		// A downloaded script piped into a shell runs code nobody read. The pipe
		// is the whole rule: `curl -o file url; sh file` is the same risk but
		// leaves a file to inspect, and `curl url | jq` is a download.
		if seg.Pipe && downloaders[name] && i+1 < len(segments) {
			if next, _ := segments[i+1].Command(); shells[next] {
				add(RuleRemoteCodeExec, SeverityHigh)
			}
		}
		if rule, sev, ok := classifySegment(name, args); ok {
			add(rule, sev)
		}
	}
	return flags
}

// classifySegment applies the per-command rules to one argument vector.
func classifySegment(name string, args []string) (string, Severity, bool) {
	switch name {
	case "sudo", "doas":
		return RulePrivilegeEscalation, SeverityHigh, true
	case "rm":
		if sev, ok := recursiveDelete(args); ok {
			return RuleRecursiveDelete, sev, true
		}
	case "chmod":
		for _, a := range args {
			if a == "777" || a == "a+rwx" || a == "-R777" {
				return RuleWorldWritable, SeverityMedium, true
			}
		}
	case "git":
		return gitRule(args)
	}
	return "", "", false
}

// gitRule classifies a git invocation by its subcommand.
//
// A dry run is excluded everywhere: `git clean -dff --dry-run` is how you find
// out what a clean would remove, and flagging it teaches the reader to ignore
// the rule.
func gitRule(args []string) (string, Severity, bool) {
	sub, rest := gitSubcommand(args)
	if sub == "" || hasWord(rest, "--dry-run") || hasWord(rest, "-n") {
		return "", "", false
	}
	switch sub {
	case "push":
		// --force-with-lease refuses to overwrite work the pusher has not seen,
		// which is the failure mode --force is flagged for.
		if hasWord(rest, "--force") || hasWord(rest, "-f") {
			return RuleForcePush, SeverityHigh, true
		}
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") && protectedBranches[a] {
				return RuleProtectedBranchPush, SeverityMedium, true
			}
		}
	case "reset":
		if hasWord(rest, "--hard") {
			return RuleHardReset, SeverityMedium, true
		}
	case "filter-branch", "filter-repo":
		return RuleHistoryRewrite, SeverityHigh, true
	}
	return "", "", false
}

// gitSubcommand finds the subcommand past git's own options, so
// `git -C /path push --force` is a push rather than an unrecognised `-C`.
func gitSubcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return a, args[i+1:]
		}
		// The global options that take a separate value; skipping only the flag
		// would read its argument as the subcommand.
		if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" {
			i++
		}
	}
	return "", nil
}

// recursiveDelete reports whether an rm is recursive and forced, and how much
// it matters.
//
// Both flags are required. `rm -r` alone stops on the first thing it cannot
// remove, and `rm -f` alone takes no directories; it is the pair that deletes a
// tree without asking. The flags may be bundled (`-rf`, `-fr`) or separate.
func recursiveDelete(args []string) (Severity, bool) {
	var recursive, force bool
	var targets []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			targets = append(targets, a)
			continue
		}
		if strings.HasPrefix(a, "--") {
			switch a {
			case "--recursive":
				recursive = true
			case "--force":
				force = true
			}
			continue
		}
		for _, c := range a[1:] {
			switch c {
			case 'r', 'R':
				recursive = true
			case 'f':
				force = true
			}
		}
	}
	if !recursive || !force {
		return "", false
	}
	// Every target expected to be thrown away -- a temp directory, a build
	// directory, anything relative to the project -- yields nothing, and so does
	// a target csm cannot resolve. Only a path it can say is outside all of
	// those produces a flag.
	worst := Severity("")
	for _, t := range targets {
		if s, ok := deleteSeverity(t); ok && s.rank() > worst.rank() {
			worst = s
		}
	}
	return worst, worst != ""
}

// ephemeralPrefixes are locations whose contents are expected to be thrown
// away. A recursive delete inside one is routine.
var ephemeralPrefixes = []string{"/tmp/", "/private/tmp/", "/var/folders/", "/var/tmp/"}

// buildDirs are the directory names a project regenerates. Deleting one is a
// clean, not a loss.
var buildDirs = map[string]bool{
	".build": true, "build": true, "dist": true, "target": true, "out": true,
	"node_modules": true, "vendor": true, "coverage": true, ".next": true,
	".venv": true, "__pycache__": true, ".pytest_cache": true, ".gradle": true,
}

// deleteSeverity ranks one delete target, and reports whether it is worth a
// flag at all.
//
// The question is what cannot be recreated. A build directory, a temp
// directory, or a path relative to the project the agent is working in are all
// things an agent removes as a matter of course, measured across a real corpus:
// 54 of the 56 recursive deletes in it were one of those three, and not one was
// worth reading. They yield no flag. What is left is a delete csm can say is
// outside the working directory -- someone else's data -- and the two targets
// that are the accident this rule exists for.
//
// Known gap: `rm -rf src` is relative and therefore silent. Telling it from
// `rm -rf tmpdir` needs to know what the project tracks, which means running
// git, which this does not do. A wrong flag on every clean would cost more than
// this misses.
func deleteSeverity(target string) (Severity, bool) {
	t := strings.TrimSuffix(target, "/")
	switch t {
	case "", ".", "..", "/", "/*", "~", "~/*", "$HOME", "$HOME/*", "*":
		return SeverityCritical, true
	}
	if base := t[strings.LastIndexByte(t, '/')+1:]; buildDirs[base] {
		return "", false
	}
	for _, p := range ephemeralPrefixes {
		if strings.HasPrefix(t, p) {
			return "", false
		}
	}
	if strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~") || strings.HasPrefix(t, "$HOME") {
		return SeverityHigh, true
	}
	return "", false
}

func hasWord(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
