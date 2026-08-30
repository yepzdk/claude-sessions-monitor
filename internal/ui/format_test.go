package ui

import (
	"strings"
	"testing"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// stripANSI returns what a terminal actually draws, without the colour codes.
func stripANSI(s string) string {
	var out []rune
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			out = append(out, r)
		}
	}
	return string(out)
}

// visibleWidth counts the runes a terminal actually draws, ignoring ANSI
// colour codes.
func visibleWidth(s string) int {
	return len([]rune(stripANSI(s)))
}

// The project column is padded to a fixed width. Measuring that padding in
// bytes while truncating in runes shifts every column to the right of a
// non-ASCII project name.
func TestFormatProjectPadsToRuneWidth(t *testing.T) {
	const width = 30
	for _, name := range []string{
		"my-api-service",
		"проект-мониторинг",
		"项目-监控",
		"café-app",
		"a",
		strings.Repeat("ü", 60),
	} {
		got := visibleWidth(formatProject(session.Session{Project: name}, width, false))
		if got != width {
			t.Errorf("project %q: visible width = %d, want %d", name, got, width)
		}
	}
}

// The cell is padded to a fixed width whether or not the badge is in it, or
// every column to its right shifts on a mixed dashboard.
func TestFormatOriginPadsToRuneWidth(t *testing.T) {
	for _, showHarness := range []bool{false, true} {
		width := fixedOriginWidth
		if showHarness {
			width += harnessBadgeWidth
		}
		for _, display := range []string{"Ghostty", "", "iTerm", "Zed", "Ünicode", "非常長的名字"} {
			s := session.Session{
				Origin:  session.Origin{Display: display},
				Harness: session.HarnessOMP,
			}
			got := visibleWidth(formatOrigin(s, width, showHarness))
			if got != width {
				t.Errorf("origin %q (harness=%v): visible width = %d, want %d",
					display, showHarness, got, width)
			}
		}
	}
}

// The agent badge belongs with the origin -- both answer "where did this come
// from" -- but after it, one space behind: the origin is the column's subject
// and the badge qualifies it, so it has to read as attached to the name rather
// than as a field of its own.
func TestFormatOriginCarriesHarnessAfterTheName(t *testing.T) {
	const width = fixedOriginWidth + harnessBadgeWidth
	cell := func(display string, h session.Harness) string {
		return stripANSI(formatOrigin(session.Session{
			Origin:  session.Origin{Display: display, Category: session.OriginTerminal},
			Harness: h,
		}, width, true))
	}

	got := cell("Ghostty", session.HarnessOMP)
	if !strings.HasPrefix(got, "Ghostty [omp]") {
		t.Errorf("cell = %q, want the badge one space after the origin name", got)
	}
	if visibleWidth(got) != width {
		t.Errorf("visible width = %d, want %d; the columns after it shift",
			visibleWidth(got), width)
	}

	// A shorter origin keeps the badge next to it rather than parked at a
	// column position of its own.
	short := cell("Zed", session.HarnessClaude)
	if !strings.HasPrefix(short, "Zed [cc]") {
		t.Errorf("cell = %q, want %q", short, "Zed [cc]")
	}
	if visibleWidth(short) != width {
		t.Errorf("short cell width = %d, want %d", visibleWidth(short), width)
	}

	// The name is capped so the longest badge still fits the widened column.
	long := cell("GNOME Terminal", session.HarnessOMP)
	if !strings.HasPrefix(long, "GNOME Term [omp]") {
		t.Errorf("cell = %q; the name should cap at %d to leave room", long, fixedOriginWidth)
	}

	plain := stripANSI(formatOrigin(session.Session{
		Origin:  session.Origin{Display: "Ghostty"},
		Harness: session.HarnessOMP,
	}, fixedOriginWidth, false))
	if strings.Contains(plain, "omp") {
		t.Errorf("agent shown on a single-agent dashboard: %q", plain)
	}
	if visibleWidth(plain) != fixedOriginWidth {
		t.Errorf("single-agent cell width = %d, want the unchanged %d",
			visibleWidth(plain), fixedOriginWidth)
	}
}

// Below originColumnMinTTY the origin column is dropped, and with it the cell
// the agent now lives in. It has to fall back to the project cell: on a mixed
// dashboard an untagged row is an ambiguous row, whatever the terminal width.
func TestRenderSessionRowKeepsHarnessOnNarrowTerminal(t *testing.T) {
	s := session.Session{
		Project: "work/api",
		Status:  session.StatusWorking,
		Harness: session.HarnessOMP,
		Origin:  session.Origin{Display: "Ghostty", Category: session.OriginTerminal},
	}

	wide := calcSessionLayout(120, true)
	if wide.origin == 0 {
		t.Fatal("120 columns should keep the origin column")
	}
	narrow := calcSessionLayout(originColumnMinTTY-1, true)
	if narrow.origin != 0 {
		t.Fatal("below originColumnMinTTY the origin column should be dropped")
	}

	for name, l := range map[string]sessionLayout{"wide": wide, "narrow": narrow} {
		var buf strings.Builder
		renderSessionRow(&buf, s, l, "\n", "  ", true)
		if !strings.Contains(stripANSI(buf.String()), "omp") {
			t.Errorf("%s layout lost the agent label:\n%s", name, stripANSI(buf.String()))
		}
	}
}

// A row whose log could not be fully read must say so, or its numbers read as
// measurements.
func TestFormatProjectMarksDegradedRow(t *testing.T) {
	out := formatProject(session.Session{Project: "api", Degraded: "permission denied"}, 30, false)
	if !strings.Contains(out, "[?]") {
		t.Errorf("degraded session is not marked: %q", out)
	}
	if visibleWidth(out) != 30 {
		t.Errorf("visible width = %d, want 30", visibleWidth(out))
	}
}

// The tag is what tells a mixed dashboard's rows apart, so it must render, must
// not disturb the column width, and must stay away when there is only one agent
// to see -- a single-agent user should get exactly the dashboard they had.
func TestFormatProjectShowsHarnessTagOnlyWhenAsked(t *testing.T) {
	const width = 30
	s := session.Session{Project: "api", Harness: session.HarnessOMP}

	tagged := formatProject(s, width, true)
	if !strings.Contains(tagged, "[omp]") {
		t.Errorf("harness tag missing: %q", tagged)
	}
	if visibleWidth(tagged) != width {
		t.Errorf("tagged cell width = %d, want %d; the columns after it shift",
			visibleWidth(tagged), width)
	}

	plain := formatProject(s, width, false)
	if strings.Contains(plain, "[omp]") {
		t.Errorf("harness tag shown on a single-agent dashboard: %q", plain)
	}
}

// The tag survives a narrow column longer than the branch and title do: on a
// mixed dashboard, which agent a row belongs to matters more than either.
func TestFormatProjectKeepsHarnessTagWhenCrowded(t *testing.T) {
	s := session.Session{
		Project:      "some-long-project-name",
		Harness:      session.HarnessClaude,
		GitBranch:    "feature/very-long-branch",
		SessionTitle: "a session title that will not fit",
	}

	out := formatProject(s, 16, true)
	if !strings.Contains(out, "[cc]") {
		t.Errorf("harness tag was dropped before the branch and title: %q", out)
	}
	if visibleWidth(out) != 16 {
		t.Errorf("visible width = %d, want 16", visibleWidth(out))
	}
}

func TestFilterByHarness(t *testing.T) {
	in := []session.Session{
		{Project: "a", Harness: session.HarnessClaude},
		{Project: "b", Harness: session.HarnessOMP},
		{Project: "c", Harness: session.HarnessClaude},
	}

	if got := FilterByHarness(in, ""); len(got) != 3 {
		t.Errorf("no filter returned %d of 3 sessions", len(got))
	}
	got := FilterByHarness(in, session.HarnessOMP)
	if len(got) != 1 || got[0].Project != "b" {
		t.Errorf("filtered to omp = %+v, want just b", got)
	}
}

// A filter outlives the mixed dashboard that allowed it: filter to the other
// agent, let its sessions go idle, and `Mixed` is false again while the rows are
// still hidden. If the footer stops naming `f` there, nothing on screen says how
// to get the rows back and restarting csm is the only way out.
func TestLiveHelpKeysAlwaysOffersTheKeyOutOfAFilter(t *testing.T) {
	if got := liveHelpKeys(false, session.HarnessClaude); !strings.Contains(got, "f: filter") {
		t.Errorf("footer = %q; a filtered view with no `f` offered cannot be undone", got)
	}
	if got := liveHelpKeys(true, ""); !strings.Contains(got, "f: filter") {
		t.Errorf("footer = %q, want `f` offered on a mixed dashboard", got)
	}
	// A single-agent machine with no filter: the key does nothing, so naming it
	// is noise.
	if got := liveHelpKeys(false, ""); strings.Contains(got, "f: filter") {
		t.Errorf("footer = %q, want no filter key on a single-agent dashboard", got)
	}
}

// "No active sessions." while your own session is running and merely filtered
// out reads as a broken scan. That is the report this fix came from.
func TestEmptyLiveMessageNamesTheFilterThatHidTheRows(t *testing.T) {
	got := emptyLiveMessage(session.HarnessClaude)
	if !strings.Contains(got, "cc") {
		t.Errorf("message = %q, want the filter named", got)
	}
	if !strings.Contains(got, "f") {
		t.Errorf("message = %q, want the key that clears it", got)
	}

	if got := emptyLiveMessage(""); got != "No active sessions." {
		t.Errorf("unfiltered message = %q, want the plain sentence", got)
	}
}

func TestActiveSessionsKeepsGhosts(t *testing.T) {
	in := []session.Session{
		{Project: "orphan", Status: session.StatusWaiting, IsGhost: true},
		{Project: "done", Status: session.StatusInactive},
		{Project: "live", Status: session.StatusWorking},
	}
	got := ActiveSessions(in)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2 (ghost kept, inactive dropped)", len(got))
	}
	if !got[0].IsGhost {
		t.Error("ghost session was filtered out; its [ghost] badge can never render")
	}
}

// A history row whose log stopped short must say so, or its "0s, 0 msgs" reads
// as a measurement. The marker also has to fit the project column, or every
// column to its right shifts.
func TestHistoryProjectCellMarksUnreadableLog(t *testing.T) {
	const width = 20
	good := historyProjectCell(session.HistorySession{Project: "api"}, width)
	if visibleWidth(good) != width {
		t.Errorf("plain cell width = %d, want %d", visibleWidth(good), width)
	}

	// A short name so the cell has to pad around the marker: padding it by
	// byte count instead measures the marker's colour codes as visible.
	marked := historyProjectCell(session.HistorySession{
		Project:  "api",
		Degraded: "scan stopped early",
	}, width)
	if !strings.Contains(marked, "[?]") {
		t.Errorf("degraded row is not marked: %q", marked)
	}
	if visibleWidth(marked) != width {
		t.Errorf("marked cell width = %d, want %d; the columns after it shift", visibleWidth(marked), width)
	}
}
