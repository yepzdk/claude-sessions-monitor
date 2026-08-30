package main

import "testing"

// `csm upgrade` and `csm update` used to start the dashboard: flag stops parsing
// at the first non-flag argument, and nothing looked at what it left behind. The
// user who typed it saw a session list appear and had no way to tell that the
// word had been thrown away.
func TestResolveArgsAcceptsTheUpgradeSubcommand(t *testing.T) {
	for _, word := range []string{"upgrade", "update"} {
		upgrade, err := resolveArgs([]string{word})
		if err != nil {
			t.Errorf("resolveArgs([%q]) = %v, want an upgrade", word, err)
		}
		if !upgrade {
			t.Errorf("resolveArgs([%q]) did not ask for an upgrade, so csm would start the dashboard instead", word)
		}
	}
}

func TestResolveArgsLeavesNormalInvocationsAlone(t *testing.T) {
	upgrade, err := resolveArgs(nil)
	if err != nil || upgrade {
		t.Errorf("resolveArgs(nil) = (%v, %v), want (false, nil)", upgrade, err)
	}
}

// An argument csm does not understand has to be refused rather than dropped:
// ignoring it means running some other command than the one that was typed.
func TestResolveArgsRejectsWhatItCannotRun(t *testing.T) {
	for _, args := range [][]string{
		{"upgrades"},         // a near-miss, which must not silently start the dashboard
		{"--upgrade=please"}, // flag already refused it; it reaches here as a positional
		{"history"},
	} {
		if _, err := resolveArgs(args); err == nil {
			t.Errorf("resolveArgs(%q) was accepted, so csm would do something else instead", args)
		}
	}

	// Naming the extra argument matters: "upgrade takes no arguments" is
	// actionable where "unknown argument" would blame the wrong word.
	_, err := resolveArgs([]string{"upgrade", "now"})
	if err == nil {
		t.Fatal("resolveArgs([upgrade now]) was accepted")
	}
	if got := err.Error(); got != `upgrade takes no arguments, got "now"` {
		t.Errorf("error = %q, which does not name the argument that is wrong", got)
	}
}
