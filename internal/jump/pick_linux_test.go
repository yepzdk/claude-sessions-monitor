//go:build linux

package jump

import "testing"

func TestPickWindowByProcessOwnership(t *testing.T) {
	// A terminal that forks per window: the window's pid is an ancestor of the
	// Claude process, and nothing else matches.
	wins := []window{
		{ID: "0xa", PID: 1000, Title: "~/Projects"},
		{ID: "0xb", PID: 2000, Title: "claude — fix the parser"},
		{ID: "0xc", PID: 3000, Title: "Spotify"},
	}
	chain := []int{5000, 4000, 2000, 1} // claude, shell, terminal, init

	got, matches, ok := pickWindow(wins, chain, "")
	if !ok {
		t.Fatal("pickWindow() found nothing, want the window owned by pid 2000")
	}
	if got.ID != "0xb" {
		t.Errorf("chose %q, want 0xb", got.ID)
	}
	if matches != 1 {
		t.Errorf("matches = %d, want 1 — an exact match is not a guess", matches)
	}
}

func TestPickWindowNoOwner(t *testing.T) {
	wins := []window{{ID: "0xa", PID: 1000, Title: "~/Projects"}}

	// A session running in a multiplexer or over SSH has no window here.
	if _, matches, ok := pickWindow(wins, []int{5000, 4000, 1}, ""); ok || matches != 0 {
		t.Errorf("pickWindow() = (matches %d, ok %v), want (0, false)", matches, ok)
	}
}

func TestPickWindowSingleInstanceTerminal(t *testing.T) {
	// Ghostty with --gtk-single-instance: every window reports the same pid,
	// so ownership narrows to all of them and cannot go further.
	wins := []window{
		{ID: "0xa", PID: 28701, Title: "◐ Reviewing the changelog"},
		{ID: "0xb", PID: 28701, Title: "π > Port project to Omarchy"},
		{ID: "0xc", PID: 28701, Title: "⏸ Rollercoaster | cliamp"},
	}
	chain := []int{5000, 4000, 28701, 1}

	// Three plausible windows and no way to separate them: declining beats
	// stealing focus to a window chosen at random.
	got, matches, ok := pickWindow(wins, chain, "")
	if ok {
		t.Errorf("pickWindow() chose %q from three indistinguishable windows, want no choice", got.ID)
	}
	if matches != 3 {
		t.Errorf("matches = %d, want 3 so the caller can say what happened", matches)
	}
}

// The case that made jumping unusable on Omarchy, which starts Ghostty with
// --gtk-single-instance=true: several agent windows under one pid, none of them
// a plain shell, so the "exactly one window is not a path" rule separates
// nothing. The session's own title does.
func TestPickWindowIdentifiesTheWindowByTheSessionTitle(t *testing.T) {
	wins := []window{
		{ID: "0xa", PID: 28701, Title: "~"},
		{ID: "0xb", PID: 28701, Title: "π > Port project to Omarchy Linux"},
		{ID: "0xc", PID: 28701, Title: "π ⠏ Fix harness PID confidence and terminal jump"},
		{ID: "0xd", PID: 28701, Title: "⏸ Rollercoaster - RUD, Elektronomia | cliamp"},
	}
	chain := []int{544041, 28701, 1}

	got, matches, ok := pickWindow(wins, chain, "Fix harness PID confidence and terminal jump")
	if !ok {
		t.Fatal("pickWindow() declined a window whose title carries the session's own title")
	}
	if got.ID != "0xc" {
		t.Errorf("chose %q, want 0xc — the window whose title holds this session's", got.ID)
	}
	// The title is per-window evidence, unlike the pid every window shares, so
	// the pick is an identification and must not be reported as a guess.
	if matches != 1 {
		t.Errorf("matches = %d, want 1: a unique title hit is not a guess", matches)
	}
}

// Claude Code decorates the title differently from omp, and a session that has
// just changed state re-decorates it. Containment is what survives that; an
// equality test would decline every one of these.
func TestPickWindowSeesThroughAgentTitleDecoration(t *testing.T) {
	chain := []int{5000, 28701, 1}
	for _, title := range []string{
		"✳ Fix the pagination bug",
		"π ⠦ Fix the pagination bug",
		"Fix the pagination bug — myproject",
		"fix the pagination bug",
	} {
		wins := []window{
			{ID: "0xa", PID: 28701, Title: "◐ Reviewing the changelog"},
			{ID: "0xb", PID: 28701, Title: title},
		}
		got, _, ok := pickWindow(wins, chain, "Fix the pagination bug")
		if !ok || got.ID != "0xb" {
			t.Errorf("window titled %q: pickWindow() = (%q, ok %v), want 0xb", title, got.ID, ok)
		}
	}
}

// A title that identifies nothing must not be allowed to pick a window: the
// user would be thrown into a session that is not theirs with no way to tell.
func TestPickWindowRefusesATitleThatSeparatesNothing(t *testing.T) {
	chain := []int{5000, 28701, 1}

	// Two windows on the same session -- a second window opened on the same
	// project, or a title generic enough to appear in a neighbour's.
	shared := []window{
		{ID: "0xa", PID: 28701, Title: "π > Fix the tests"},
		{ID: "0xb", PID: 28701, Title: "✳ Fix the tests, part two"},
	}
	if got, matches, ok := pickWindow(shared, chain, "Fix the tests"); ok {
		t.Errorf("pickWindow() chose %q from two windows the title matches equally (matches=%d)", got.ID, matches)
	}

	// A title too short to mean anything appears inside unrelated titles by
	// coincidence, so it is not trusted even when it hits exactly once.
	short := []window{
		{ID: "0xa", PID: 28701, Title: "◐ wip on the parser"},
		{ID: "0xb", PID: 28701, Title: "⏸ Rollercoaster | cliamp"},
	}
	if got, _, ok := pickWindow(short, chain, "wip"); ok {
		t.Errorf("pickWindow() trusted a 3-character title and chose %q", got.ID)
	}
}

// The plain-shell heuristic is the fallback, not the primary rule: a session
// with no title of its own must still land where it used to.
func TestPickWindowFallsBackToThePlainShellRuleWithoutATitle(t *testing.T) {
	wins := []window{
		{ID: "0xa", PID: 28701, Title: "…/Projects/personal/webwrap"},
		{ID: "0xb", PID: 28701, Title: "◐ Reviewing the changelog"},
	}

	got, matches, ok := pickWindow(wins, []int{5000, 28701, 1}, "")
	if !ok || got.ID != "0xb" {
		t.Fatalf("pickWindow() = (%q, ok %v), want 0xb", got.ID, ok)
	}
	if matches != 2 {
		t.Errorf("matches = %d, want 2 — chosen from two candidates, so still a guess", matches)
	}
}

func TestPickWindowGuessesPastPlainShells(t *testing.T) {
	// The common shape of the ambiguous case: one window running something,
	// the rest idle shells echoing their working directory.
	wins := []window{
		{ID: "0xa", PID: 28701, Title: "…/Projects/personal/webwrap"},
		{ID: "0xb", PID: 28701, Title: "◐ Reviewing the changelog"},
		{ID: "0xc", PID: 28701, Title: "~/Work"},
	}
	chain := []int{5000, 4000, 28701, 1}

	got, matches, ok := pickWindow(wins, chain, "")
	if !ok {
		t.Fatal("pickWindow() found nothing, want the one window that is not an idle shell")
	}
	if got.ID != "0xb" {
		t.Errorf("chose %q, want 0xb", got.ID)
	}
	// The caller must hear that this was a guess among three.
	if matches != 3 {
		t.Errorf("matches = %d, want 3 so the result reads as a best guess", matches)
	}
}

func TestPickWindowIgnoresWindowsWithoutPIDs(t *testing.T) {
	// wmctrl reports 0 for windows whose owner it cannot determine. A zero pid
	// must never match a zero entry in the chain.
	wins := []window{
		{ID: "0xa", PID: 0, Title: "some window"},
		{ID: "0xb", PID: 2000, Title: "terminal"},
	}

	got, _, ok := pickWindow(wins, []int{0, 2000}, "")
	if !ok || got.ID != "0xb" {
		t.Errorf("pickWindow() = (%q, ok %v), want 0xb", got.ID, ok)
	}
}

func TestPickWindowStopsAtTheNearestOwningAncestor(t *testing.T) {
	// `ghostty &` launched from a shell running in kitty, claude started in
	// the ghostty: the chain reaches both terminals, and each owns a window.
	// Collecting from every ancestor would report two candidates and blame a
	// single-instance terminal that is not involved.
	wins := []window{
		{ID: "0xkitty", PID: 1000, Title: "nvim"},
		{ID: "0xghostty", PID: 2000, Title: "◐ Fixing tests"},
	}
	chain := []int{5000, 4000, 2000, 3000, 1000, 1} // claude, zsh, ghostty, zsh, kitty, init

	got, matches, ok := pickWindow(wins, chain, "")
	if !ok {
		t.Fatal("pickWindow() declined, want the nearest ancestor's window")
	}
	if got.ID != "0xghostty" {
		t.Errorf("chose %q, want 0xghostty — the terminal the session runs in", got.ID)
	}
	if matches != 1 {
		t.Errorf("matches = %d, want 1: the ancestor kitty window is not a candidate", matches)
	}
}

func TestPickWindowIgnoresCSMsOwnWindow(t *testing.T) {
	// Single-instance Ghostty with csm running in one of its windows. csm's
	// window shares the pid but is certainly not the session's — it is where
	// the user just pressed Enter — so the session's window is unambiguous.
	wins := []window{
		{ID: "0xcsm", PID: 28701, Title: "CSM: 2 working"},
		{ID: "0xsession", PID: 28701, Title: "~/proj"},
	}
	chain := []int{5000, 4000, 28701, 1}

	got, matches, ok := pickWindow(wins, chain, "")
	if !ok {
		t.Fatal("pickWindow() declined; csm's own window should not make the choice ambiguous")
	}
	if got.ID != "0xsession" {
		t.Errorf("chose %q, want 0xsession", got.ID)
	}
	if matches != 1 {
		t.Errorf("matches = %d, want 1 — csm's window is not something the user chose between", matches)
	}
}

func TestPickWindowDoesNotLetCSMSuppressTheGuess(t *testing.T) {
	// The fallback picks the one candidate that is not an idle shell. csm's
	// title is not a path either, so counting it as a named candidate stopped
	// the fallback from ever firing on a terminal shared with the dashboard.
	wins := []window{
		{ID: "0xcsm", PID: 28701, Title: "CSM: 1 working"},
		{ID: "0xshell", PID: 28701, Title: "…/Projects/webwrap"},
		{ID: "0xsession", PID: 28701, Title: "◐ Reviewing the changelog"},
	}
	chain := []int{5000, 4000, 28701, 1}

	got, matches, ok := pickWindow(wins, chain, "")
	if !ok || got.ID != "0xsession" {
		t.Errorf("pickWindow() = (%q, ok %v), want 0xsession", got.ID, ok)
	}
	if matches != 2 {
		t.Errorf("matches = %d, want 2 — the count the user is shown excludes csm's window", matches)
	}
}
