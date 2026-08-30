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

func TestFormatOriginPadsToRuneWidth(t *testing.T) {
	const width = 14
	for _, display := range []string{"Ghostty", "", "iTerm", "Zed", "Ünicode", "非常長的名字"} {
		for _, showHarness := range []bool{false, true} {
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

// The agent belongs with the origin: both answer "where did this come from".
// The prefix is padded to a fixed width so a `cc` row and an `omp` row line
// their origin names up down the column.
func TestFormatOriginCarriesHarness(t *testing.T) {
	const width = 14
	omp := formatOrigin(session.Session{
		Origin:  session.Origin{Display: "Ghostty", Category: session.OriginTerminal},
		Harness: session.HarnessOMP,
	}, width, true)
	claude := formatOrigin(session.Session{
		Origin:  session.Origin{Display: "Ghostty", Category: session.OriginTerminal},
		Harness: session.HarnessClaude,
	}, width, true)

	if !strings.Contains(omp, "omp") || !strings.Contains(omp, "Ghostty") {
		t.Errorf("omp cell = %q, want both the agent and the origin", omp)
	}
	if !strings.Contains(claude, "cc") {
		t.Errorf("claude cell = %q, want the agent label", claude)
	}
	// Same origin, same column position for the name.
	if strings.Index(stripANSI(omp), "Ghostty") != strings.Index(stripANSI(claude), "Ghostty") {
		t.Errorf("origin names do not align: %q vs %q", stripANSI(omp), stripANSI(claude))
	}

	plain := formatOrigin(session.Session{
		Origin:  session.Origin{Display: "Ghostty"},
		Harness: session.HarnessOMP,
	}, width, false)
	if strings.Contains(plain, "omp") {
		t.Errorf("agent shown on a single-agent dashboard: %q", plain)
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

	wide := calcSessionLayout(120)
	if wide.origin == 0 {
		t.Fatal("120 columns should keep the origin column")
	}
	narrow := calcSessionLayout(originColumnMinTTY - 1)
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

func TestMixedHarnesses(t *testing.T) {
	claudeOnly := []session.Session{
		{Harness: session.HarnessClaude}, {Harness: session.HarnessClaude},
	}
	if MixedHarnesses(claudeOnly) {
		t.Error("one agent reported as mixed; every row would carry a pointless tag")
	}

	both := []session.Session{
		{Harness: session.HarnessClaude}, {Harness: session.HarnessOMP},
	}
	if !MixedHarnesses(both) {
		t.Error("two agents not reported as mixed; the rows would be ambiguous")
	}

	if MixedHarnesses(nil) {
		t.Error("an empty dashboard reported as mixed")
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
