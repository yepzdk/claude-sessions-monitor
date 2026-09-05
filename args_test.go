package main

import (
	"flag"
	"io"
	"slices"
	"testing"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// parseArgs runs a command line through the same flags and the same resolver
// csm uses, and reports what they made of it.
func parseArgs(t *testing.T, args ...string) (*options, bool, error) {
	t.Helper()
	fs := flag.NewFlagSet("csm", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := registerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return opts, false, err
	}
	upgrade, err := resolveArgs(fs.Args(), fs)
	return opts, upgrade, err
}

// `csm upgrade` and `csm update` used to start the dashboard: flag stops parsing
// at the first non-flag argument, and nothing looked at what it left behind. The
// user who typed it saw a session list appear and had no way to tell that the
// word had been thrown away.
func TestResolveArgsAcceptsTheUpgradeSubcommand(t *testing.T) {
	for _, word := range []string{"upgrade", "update"} {
		_, upgrade, err := parseArgs(t, word)
		if err != nil {
			t.Errorf("csm %s = %v, want an upgrade", word, err)
		}
		if !upgrade {
			t.Errorf("csm %s did not ask for an upgrade, so csm would start the dashboard instead", word)
		}
	}
}

func TestResolveArgsLeavesNormalInvocationsAlone(t *testing.T) {
	_, upgrade, err := parseArgs(t)
	if err != nil || upgrade {
		t.Errorf("csm = (%v, %v), want (false, nil)", upgrade, err)
	}
}

// flag stops at the first non-flag word, so everything after `upgrade` used to
// be reported as an argument csm cannot run -- including flags csm has.
func TestResolveArgsAcceptsFlagsAfterTheSubcommand(t *testing.T) {
	opts, upgrade, err := parseArgs(t, "upgrade", "-v")
	if err != nil {
		t.Fatalf("csm upgrade -v = %v, want the version flag to be honoured", err)
	}
	if !upgrade || !opts.showVersion {
		t.Errorf("csm upgrade -v = (upgrade %v, -v %v), want both", upgrade, opts.showVersion)
	}

	opts, upgrade, err = parseArgs(t, "upgrade", "-y")
	if err != nil {
		t.Fatalf("csm upgrade -y = %v", err)
	}
	if !upgrade || !opts.assumeYes {
		t.Errorf("csm upgrade -y = (upgrade %v, -y %v), want both", upgrade, opts.assumeYes)
	}

	// --yes is the same variable under its long spelling.
	if opts, _, err := parseArgs(t, "--yes", "upgrade"); err != nil || !opts.assumeYes {
		t.Errorf("csm --yes upgrade = (%v, %v), want --yes honoured", opts.assumeYes, err)
	}
}

// An argument csm does not understand has to be refused rather than dropped:
// ignoring it means running some other command than the one that was typed.
func TestResolveArgsRejectsWhatItCannotRun(t *testing.T) {
	for _, args := range [][]string{
		{"upgrades"},         // a near-miss, which must not silently start the dashboard
		{"--upgrade=please"}, // flag already refused it; it reaches here as a positional
		{"history"},
		{"upgrade", "update"}, // the word twice is still a word too many
	} {
		if _, _, err := parseArgs(t, args...); err == nil {
			t.Errorf("csm %q was accepted, so csm would do something else instead", args)
		}
	}

	// Naming the extra argument matters: "upgrade takes no arguments" is
	// actionable where "unknown argument" would blame the wrong word. Re-parsing
	// the remainder must not cost this message.
	_, _, err := parseArgs(t, "upgrade", "now")
	if err == nil {
		t.Fatal("csm upgrade now was accepted")
	}
	if got := err.Error(); got != `upgrade takes no arguments, got "now"` {
		t.Errorf("error = %q, which does not name the argument that is wrong", got)
	}

	// The word that was typed is the word that is named.
	if _, _, err := parseArgs(t, "update", "now"); err == nil ||
		err.Error() != `update takes no arguments, got "now"` {
		t.Errorf("csm update now = %v, want the error to name `update`", err)
	}
}

// `csm -l upgrade` upgraded and dropped the -l without a word, which is the same
// "ran a different command than the one typed" the argument errors prevent.
func TestUpgradeRefusesFlagsItCannotHonour(t *testing.T) {
	for _, args := range [][]string{
		{"-l", "upgrade"},
		{"upgrade", "-l"},
		{"-web", "-upgrade"},
		{"-interval", "5s", "upgrade"},
	} {
		fs := flag.NewFlagSet("csm", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		opts := registerFlags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatalf("csm %q: %v", args, err)
		}
		upgrade, err := resolveArgs(fs.Args(), fs)
		if err != nil {
			t.Fatalf("csm %q: %v", args, err)
		}
		if !upgrade && !opts.doUpgrade {
			t.Fatalf("csm %q did not ask for an upgrade", args)
		}
		if err := upgradeConflicts(fs); err == nil {
			t.Errorf("csm %q was accepted, so the flag would be silently ignored", args)
		}
	}

	// -v and -y are the two flags an upgrade can honour: -v prints the version
	// and exits before it, -y answers its confirmation.
	for _, args := range [][]string{
		{"upgrade"},
		{"upgrade", "-v"},
		{"upgrade", "-y"},
		{"--yes", "upgrade"},
		{"-upgrade", "-y"},
	} {
		fs := flag.NewFlagSet("csm", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		registerFlags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatalf("csm %q: %v", args, err)
		}
		if _, err := resolveArgs(fs.Args(), fs); err != nil {
			t.Fatalf("csm %q: %v", args, err)
		}
		if err := upgradeConflicts(fs); err != nil {
			t.Errorf("csm %q was refused: %v", args, err)
		}
	}
}

// The harness filter (`f`, where the footer offers it) walks every agent and
// then returns to showing all of them. A cycle that skips an agent, or never
// comes back to "", leaves rows hidden with no key that brings them back.
//
// The agents are named here rather than read from session.Harnesses: deriving
// the expectation from the same slice the code walks would pass even if an
// agent were dropped from it.
func TestHarnessFilterCyclesEveryAgentAndBackToAll(t *testing.T) {
	var seen []session.Harness
	current := session.Harness("")
	for range len(session.Harnesses()) + 1 {
		current = nextHarnessFilter(current)
		seen = append(seen, current)
	}

	for _, want := range []session.Harness{session.HarnessClaude, session.HarnessOMP} {
		if !slices.Contains(seen, want) {
			t.Errorf("the %q filter is unreachable: pressing f never selects it, so its rows cannot be brought back", want)
		}
	}
	if last := seen[len(seen)-1]; last != "" {
		t.Errorf("cycle ended at %q, want \"\": the filter never returns to showing every agent", last)
	}
}
