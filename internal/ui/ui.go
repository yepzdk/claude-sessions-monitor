// Package ui renders the terminal views (live, history, usage) and reads raw
// keyboard input. It draws with plain ANSI escapes — alt screen, cursor home,
// erase-to-end-of-line per row — so frames overwrite in place without a clear.
// Every string that originates in a log or the filesystem goes through
// sanitizeForTerminal before it is printed, and widths are counted in runes.
//
// View selection lives in main.go; this package only exposes the renderers.
// See docs/ARCHITECTURE.md for the helpers new views are expected to reuse.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yepzdk/claude-sessions-monitor/internal/session"
)

// ANSI color codes
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Red     = "\033[31m"
	Gray    = "\033[90m"
	BgGreen = "\033[42m"
)

// Status symbols (all narrow/single-column width for consistent alignment)
const (
	SymbolWorking    = "●"
	SymbolNeedsInput = "▲"
	SymbolWaiting    = "◉"
	SymbolInactive   = "◌"
)

// RenderList renders sessions as a simple list (for -l flag)
func RenderList(sessions []session.Session) {
	if len(sessions) == 0 {
		fmt.Println("No active sessions found.")
		return
	}

	l := calcSessionLayout(getTerminalWidth())
	showHarness := MixedHarnesses(sessions)

	var buf strings.Builder
	buf.WriteString(sessionHeader(l, "") + "\n")
	buf.WriteString(strings.Repeat("─", l.totalWidth) + "\n")

	for _, s := range sessions {
		renderSessionRow(&buf, s, l, "\n", "", showHarness)
	}

	fmt.Print(buf.String())
}

// MixedHarnesses reports whether these sessions come from more than one coding
// agent.
//
// It is what decides whether rows carry a harness tag. Tagging only in mixed
// company means someone running a single agent sees exactly the dashboard they
// saw before, and someone running two never has to guess which row is which --
// tagging one agent and not the other would leave the untagged rows ambiguous
// to anyone who does not already know the feature exists.
func MixedHarnesses(sessions []session.Session) bool {
	var first session.Harness
	for _, s := range sessions {
		if s.Harness == "" {
			continue
		}
		if first == "" {
			first = s.Harness
			continue
		}
		if s.Harness != first {
			return true
		}
	}
	return false
}

// FilterByHarness returns only the sessions belonging to h, or all of them when
// h is empty. It is a display filter, not a discovery one: both agents are
// always scanned, so toggling it costs nothing and never hides a session csm
// failed to find.
func FilterByHarness(sessions []session.Session, h session.Harness) []session.Session {
	if h == "" {
		return sessions
	}
	filtered := make([]session.Session, 0, len(sessions))
	for _, s := range sessions {
		if s.Harness == h {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// sessionHeader returns the column header row matching the given layout.
// gutter is the same left-edge padding the rows use, so the columns line up:
// unselectedMarker in the live view, "" for one-shot output that has no
// selection to show.
func sessionHeader(l sessionLayout, gutter string) string {
	activity := l.activity - len([]rune(gutter))
	if l.origin > 0 {
		return fmt.Sprintf("%s%-*s %-*s %-*s %-*s %-*s",
			gutter,
			l.status, "STATUS",
			l.project, "PROJECT",
			l.origin, "ORIGIN",
			l.context, "CONTEXT",
			activity, "LAST ACTIVITY")
	}
	return fmt.Sprintf("%s%-*s %-*s %-*s %-*s",
		gutter,
		l.status, "STATUS",
		l.project, "PROJECT",
		l.context, "CONTEXT",
		activity, "LAST ACTIVITY")
}

// RenderJSON renders sessions as JSON
func RenderJSON(sessions []session.Session) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(sessions)
}

// rawNewline is the line ending used for interactive (raw terminal mode)
// redraws: erase-to-end-of-line, then CRLF. Erasing per line lets a refresh
// overwrite the previous frame in place instead of clearing the whole screen
// up front — a full clear-then-redraw is what causes the visible flash/blink
// some terminals show on every refresh tick.
const rawNewline = "\033[K\r\n"

// Selection marker drawn in the left gutter of the highlighted row. The gutter
// is the same width whether or not a row is selected, so moving the cursor
// never reflows the table.
const (
	selectedMarker   = "\u258c "
	unselectedMarker = "  "
)

// ActiveSessions returns the sessions the live view shows: everything that
// isn't finished. Callers that need to address a row by index must use this,
// so the selection and the rendered table can't disagree.
//
// Orphaned sessions stay in the list. They are the ones a user most needs to
// act on, and they carry a [ghost] badge that nothing could display while they
// were filtered out here.
func ActiveSessions(sessions []session.Session) []session.Session {
	var active []session.Session
	for _, s := range sessions {
		if s.Status == session.StatusInactive {
			continue
		}
		active = append(active, s)
	}
	return active
}

// LiveView is one frame of the live dashboard. It is a struct because the frame
// outgrew a readable argument list once the harness filter joined it, and every
// field is something the footer or a row has to know.
type LiveView struct {
	// Sessions is the already-filtered set to display.
	Sessions     []session.Session
	WebURL       string // when non-empty, the footer offers the web dashboard
	ClaudeStatus *session.ClaudeStatus
	// Selected indexes ActiveSessions(Sessions), or -1 for no selection.
	Selected int
	// JumpMsg is one line of feedback from the last jump attempt, or "".
	JumpMsg string
	// Filter is the harness the view is restricted to, or "" for all.
	Filter session.Harness
	// Mixed reports whether both agents were present *before* filtering, which
	// is what decides whether rows carry a harness tag. Taken before the filter
	// so narrowing to one agent still shows which one you are looking at.
	Mixed bool
}

// RenderLive renders the live dashboard view.
// Uses \r\n for newlines to work correctly in raw terminal mode.
func RenderLive(v LiveView) {
	// Set terminal title with status summary
	SetTerminalTitle(buildTerminalTitle(v.Sessions))

	// Build the whole frame in memory and write it out in a single syscall.
	// Printing each line separately means the terminal can render partway
	// through a frame — any row whose text changed briefly shows blank (or
	// the old content) between writes, which is what "flicker on updated
	// rows" actually is. One write makes each redraw atomic from the
	// terminal's point of view.
	var buf strings.Builder

	// Move cursor to top without erasing — see rawNewline for why the screen
	// isn't cleared up front.
	buf.WriteString("\033[H")

	// Header
	fmt.Fprintf(&buf, "%sCoding Sessions%s", Bold, Reset)
	if v.Filter != "" {
		fmt.Fprintf(&buf, " %s(%s only)%s", Dim, harnessLabel(v.Filter), Reset)
	}
	fmt.Fprintf(&buf, "%s%s", rawNewline, rawNewline)

	active := ActiveSessions(v.Sessions)

	// Status summary (only active sessions)
	counts := countByStatus(active)
	fmt.Fprintf(&buf, "%s%s Working: %d%s  ", Green, SymbolWorking, counts[session.StatusWorking], Reset)
	fmt.Fprintf(&buf, "%s%s Needs Input: %d%s  ", Yellow, SymbolNeedsInput, counts[session.StatusNeedsInput], Reset)
	fmt.Fprintf(&buf, "%s%s Waiting: %d%s", Blue, SymbolWaiting, counts[session.StatusWaiting], Reset)
	buf.WriteString(rawNewline)

	buf.WriteString(rawNewline)

	if len(active) == 0 {
		fmt.Fprintf(&buf, "%sNo active sessions.%s%s", Dim, Reset, rawNewline)
	} else {
		l := calcSessionLayout(getTerminalWidth())

		// Column headers
		fmt.Fprintf(&buf, "%s%s", sessionHeader(l, unselectedMarker), rawNewline)
		fmt.Fprintf(&buf, "%s%s", strings.Repeat("─", l.totalWidth), rawNewline)

		for i, s := range active {
			marker := unselectedMarker
			if i == v.Selected {
				marker = selectedMarker
			}
			renderSessionRow(&buf, s, l, rawNewline, marker, v.Mixed)
		}
	}

	// Show Claude service status
	statusLink := terminalLink("https://status.claude.com/", "status.claude.com")
	buf.WriteString(rawNewline)
	if v.ClaudeStatus != nil && v.ClaudeStatus.Available {
		switch v.ClaudeStatus.Indicator {
		case "minor":
			fmt.Fprintf(&buf, "%s%s Claude: %s - %s%s%s", Yellow, "\u26A0", v.ClaudeStatus.Description, statusLink, Reset, rawNewline)
		case "major", "critical":
			fmt.Fprintf(&buf, "%s%s Claude: %s - %s%s%s", Red, "\u2716", v.ClaudeStatus.Description, statusLink, Reset, rawNewline)
		default:
			fmt.Fprintf(&buf, "%sClaude: %s - %s%s%s", Dim, v.ClaudeStatus.Description, statusLink, Reset, rawNewline)
		}
	} else {
		fmt.Fprintf(&buf, "%sClaude: Status unavailable - %s%s%s", Dim, statusLink, Reset, rawNewline)
	}

	// Feedback from the last jump attempt, on its own line so it never shifts
	// the table.
	if v.JumpMsg != "" {
		fmt.Fprintf(&buf, "%s%s%s%s", Dim, sanitizeForTerminal(v.JumpMsg), Reset, rawNewline)
	}

	// Show help footer. The harness filter is only offered when there is
	// something to filter: on a single-agent machine the key does nothing and
	// advertising it would be noise.
	keys := "↑↓: select | Enter: jump | h: history | u: usage"
	if v.Mixed {
		keys += " | f: filter"
	}
	if v.WebURL != "" {
		fmt.Fprintf(&buf, "%s%s | w: open webview (%s) | Ctrl+C: quit%s%s", Dim, keys, v.WebURL, Reset, rawNewline)
	} else {
		fmt.Fprintf(&buf, "%s%s | Ctrl+C: quit%s%s", Dim, keys, Reset, rawNewline)
	}

	fmt.Print(buf.String())
}

// newlineFor returns the line ending a render function should use: rawNewline
// in interactive mode (showFooter true), or a plain "\n" for one-shot,
// non-terminal output where erase-to-end-of-line escapes would just be noise.
func newlineFor(showFooter bool) string {
	if showFooter {
		return rawNewline
	}
	return "\n"
}

// MoveCursorHome moves the cursor to the top-left without erasing the
// screen. Called once before a redraw so each refresh overwrites the
// previous frame in place — see rawNewline for why a full clear-then-redraw
// causes a visible flash on some terminals.
func MoveCursorHome() {
	fmt.Print("\033[H")
}

// EraseToEnd erases from the cursor to the end of the screen. Called once
// after a full redraw completes, to clear any rows left over from a
// previous, longer frame (e.g. a session or subagent row that's no longer
// there). This is a per-render-cycle concern, not something each view needs
// to remember to do itself — see main.go's render loop for the call site.
func EraseToEnd() {
	fmt.Print("\033[J")
}

// EnterAltScreen switches to the terminal's alternate screen buffer.
// Besides preserving the user's scrollback, this is the conventional signal
// that an application is a full-screen TUI. Block-based terminals (JetBrains'
// terminal, Warp) ignore bare cursor-control sequences on the main buffer and
// only redraw in place once the alternate buffer is active.
func EnterAltScreen() {
	fmt.Print("\033[?1049h")
}

// ExitAltScreen returns to the normal screen buffer, restoring whatever was
// on screen before EnterAltScreen was called.
func ExitAltScreen() {
	fmt.Print("\033[?1049l")
}

// HideCursor hides the terminal cursor
func HideCursor() {
	fmt.Print("\033[?25l")
}

// ShowCursor shows the terminal cursor
func ShowCursor() {
	fmt.Print("\033[?25h")
}

// SetTerminalTitle sets the terminal tab/window title
// The title is sanitized to prevent terminal escape sequence injection
func SetTerminalTitle(title string) {
	fmt.Printf("\033]0;%s\007", sanitizeForTerminal(title))
}

// sanitizeForTerminal removes control characters that could be used
// for terminal escape sequence injection attacks
func sanitizeForTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1 // Remove control characters
		}
		return r
	}, s)
}

// terminalLink creates a clickable hyperlink using the OSC 8 escape sequence.
// Supported by most modern terminal emulators (iTerm2, macOS Terminal, GNOME Terminal, etc).
func terminalLink(url, text string) string {
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

// ResetTerminalTitle resets the terminal title to default
func ResetTerminalTitle() {
	fmt.Print("\033]0;\007")
}

// buildTerminalTitle creates a status summary for the terminal title
func buildTerminalTitle(sessions []session.Session) string {
	counts := countByStatus(ActiveSessions(sessions))

	// Priority: Needs Input > Working > Waiting
	var parts []string

	if n := counts[session.StatusNeedsInput]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d needs input", n))
	}
	if n := counts[session.StatusWorking]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d working", n))
	}
	if n := counts[session.StatusWaiting]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", n))
	}

	if len(parts) == 0 {
		return "CSM: no active sessions"
	}

	return "CSM: " + strings.Join(parts, ", ")
}

// getStatusDisplay returns the symbol and color for a status
func getStatusDisplay(status session.Status) (string, string) {
	switch status {
	case session.StatusWorking:
		return SymbolWorking, Green
	case session.StatusNeedsInput:
		return SymbolNeedsInput, Yellow
	case session.StatusWaiting:
		return SymbolWaiting, Blue
	case session.StatusInactive:
		return SymbolInactive, Dim
	default:
		return SymbolInactive, Reset
	}
}

// formatStatus formats the status cell with symbol and padding to exact width
func formatStatus(status session.Status, width int) string {
	symbol, color := getStatusDisplay(status)
	text := symbol + " " + string(status)
	visibleLen := 2 + len(string(status)) // symbol(1) + space(1) + status text

	// Pad to width
	if visibleLen < width {
		text += strings.Repeat(" ", width-visibleLen)
	}

	return color + text + Reset
}

// countByStatus counts sessions by their status
func countByStatus(sessions []session.Session) map[session.Status]int {
	counts := make(map[session.Status]int)
	for _, s := range sessions {
		counts[s.Status]++
	}
	return counts
}

// formatElapsed formats a duration as a human-readable elapsed time
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// truncate truncates a string to a maximum visible length (in runes, not bytes).
// This ensures multi-byte UTF-8 characters are not split mid-character.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// contextBarWidth is the number of block characters in the progress bar
const contextBarWidth = 10

// formatContext renders a visual progress bar with percentage label
// Example: "████████░░ 80%"
func formatContext(s session.Session, width int) string {
	if s.ContextTokens == 0 {
		text := "-"
		if len(text) < width {
			text += strings.Repeat(" ", width-len(text))
		}
		return Dim + text + Reset
	}

	// Clamp percentage to 0-100
	pct := s.ContextPercent
	if pct > 100 {
		pct = 100
	}

	// Calculate filled vs empty blocks
	filled := int(pct / 100 * float64(contextBarWidth))
	if filled > contextBarWidth {
		filled = contextBarWidth
	}
	empty := contextBarWidth - filled

	// Color based on percentage
	var color string
	switch {
	case pct >= 91:
		color = Red
	case pct >= 76:
		color = Yellow
	default:
		color = Green
	}

	// Build bar: colored filled blocks + dim empty blocks + percentage
	label := fmt.Sprintf(" %.0f%%", pct)

	// Append a marker when the active model uses an extended context window so
	// users can tell at a glance that "24%" is of 1M, not 200K.
	suffix := ""
	if session.ContextWindowForModel(s.Model) > session.DefaultContextWindow {
		suffix = " (1M)"
	}

	bar := color + strings.Repeat("█", filled) + Reset +
		Dim + strings.Repeat("░", empty) + Reset +
		label
	if suffix != "" {
		bar += Dim + suffix + Reset
	}

	// Pad to width (visible length = bar chars + label chars + suffix chars)
	visibleLen := contextBarWidth + len(label) + len(suffix)
	if visibleLen < width {
		bar += strings.Repeat(" ", width-visibleLen)
	}

	return bar
}

// harnessCellWidth is the room formatOrigin reserves for the agent prefix:
// the widest label plus a separating space, so origin names stay aligned down
// the column whichever agent a row belongs to.
const harnessCellWidth = 4

// formatOrigin renders the session's provenance cell — which agent, and what
// launched it — padded to exactly width visible chars. Returns an empty string
// when the column is disabled (width == 0).
//
// The agent prefix is dim so the origin name still reads first: the origin is
// the more variable fact, and the prefix repeats down the column.
func formatOrigin(s session.Session, width int, showHarness bool) string {
	if width <= 0 {
		return ""
	}

	prefix, prefixLen := "", 0
	if showHarness && s.Harness != "" {
		label := harnessLabel(s.Harness)
		// Padded to a fixed width rather than joined with a single space, so
		// `cc` and `omp` rows line their origin names up.
		if pad := harnessCellWidth - 1 - len([]rune(label)); pad > 0 {
			label += strings.Repeat(" ", pad)
		}
		prefix = Dim + label + Reset + " "
		prefixLen = harnessCellWidth
	}

	text := s.Origin.Display
	if text == "" {
		text = "-"
	}
	// Runes, not bytes: slicing by byte can split a multi-byte character in
	// half and mis-measures the padding.
	nameWidth := width - prefixLen
	if nameWidth < 1 {
		nameWidth = 1
	}
	runes := []rune(text)
	if len(runes) > nameWidth {
		runes = runes[:nameWidth]
		text = string(runes)
	}
	padding := strings.Repeat(" ", nameWidth-len(runes))

	var color string
	switch s.Origin.Category {
	case session.OriginTerminal:
		color = Gray
	case session.OriginIDE:
		color = Blue
	case session.OriginDesktop:
		color = Yellow
	default:
		color = Dim
	}
	return prefix + color + text + Reset + padding
}

// renderSessionRow renders a single session row using the given layout.
// The main row shows status, project, origin (optional), context, and activity.
// A second indented line shows the last message using the full width, followed
// by any subagent rows. marker fills the left gutter; its width is carved out
// of the row, so selected and unselected markers must be the same width.
func renderSessionRow(buf *strings.Builder, s session.Session, l sessionLayout, nl string, marker string,
	showHarness bool) {
	activity := formatElapsed(time.Since(s.LastActivity))
	if s.Status == session.StatusWorking {
		activity = "Now"
	}

	// The gutter is carved out of the activity column rather than added to the
	// row: calcSessionLayout already spends the full terminal width, so widening
	// the row here would wrap every line.
	gutter := len([]rune(marker))
	activityWidth := l.activity - gutter
	if activityWidth < 1 {
		activityWidth = 1
	}

	var row string
	if l.origin > 0 {
		// The agent rides in the origin cell: both answer "where did this come
		// from". When the terminal is too narrow for that column it falls back
		// to the project cell rather than disappearing -- on a mixed dashboard
		// an untagged row is an ambiguous row, whatever the width.
		row = fmt.Sprintf("%s%s %s %s %s %-*s",
			marker,
			formatStatus(s.Status, l.status),
			formatProject(s, l.project, false),
			formatOrigin(s, l.origin, showHarness),
			formatContext(s, l.context),
			activityWidth, activity)
	} else {
		row = fmt.Sprintf("%s%s %s %s %-*s",
			marker,
			formatStatus(s.Status, l.status),
			formatProject(s, l.project, showHarness),
			formatContext(s, l.context),
			activityWidth, activity)
	}
	buf.WriteString(row + nl)

	// Second line: last message aligned with status text (after "● ")
	// Sanitize to prevent ANSI escape injection from log content
	desc := sanitizeForTerminal(s.LastMessage)
	if desc == "" {
		desc = sanitizeForTerminal(s.Task)
	}
	if desc != "" && desc != "-" {
		indent := gutter + 2 // gutter, then align with status text (after symbol + space)
		msgWidth := l.totalWidth - indent
		if msgWidth > 0 {
			msg := truncate(desc, msgWidth)
			fmt.Fprintf(buf, "%s%s%s%s", strings.Repeat(" ", indent), Dim, msg, Reset+nl)
		}
	}

	// Nested subagent rows, indented under their parent session
	for _, sa := range s.Subagents {
		renderSubagentRow(buf, sa, l, nl, gutter)
	}

	// Blank line after each session block for visual grouping
	buf.WriteString(nl)
}

// Indentation for nested subagent rows: "  └ " before the status symbol, and
// the description line indented past it.
const (
	subagentIndent     = "  └ "
	subagentIndentLen  = 4
	subagentDescIndent = 6
)

// renderSubagentRow renders one subagent as an indented child of its session.
func renderSubagentRow(buf *strings.Builder, sa session.Subagent, l sessionLayout, nl string, gutter int) {
	activity := "Now"
	if elapsed := time.Since(sa.LastActivity); elapsed >= time.Minute {
		activity = formatElapsed(elapsed)
	}

	label := sanitizeForTerminal(sa.Label())
	if sa.Blocking {
		label += " (blocking)"
	}

	// Label column absorbs everything the fixed columns don't use, so the
	// activity column stays aligned with the parent table. The selection gutter
	// is part of that fixed cost.
	activityWidth := l.activity - gutter
	if activityWidth < 1 {
		activityWidth = 1
	}
	labelWidth := l.totalWidth - gutter - subagentIndentLen - 2 - activityWidth - 1
	if labelWidth < 1 {
		labelWidth = 1
	}
	label = truncate(label, labelWidth)

	fmt.Fprintf(buf, "%s%s%s%s%s %s%-*s%s %-*s%s",
		strings.Repeat(" ", gutter),
		subagentIndent,
		Green, SymbolWorking, Reset,
		Dim, labelWidth, label, Reset,
		activityWidth, activity,
		nl)

	desc := sanitizeForTerminal(sa.Description)
	if task := sanitizeForTerminal(sa.Task); task != "" {
		desc = task
	}
	if desc != "" && desc != "-" {
		indent := gutter + subagentDescIndent
		descWidth := l.totalWidth - indent
		if descWidth > 0 {
			fmt.Fprintf(buf, "%s%s%s%s",
				strings.Repeat(" ", indent),
				Dim, truncate(desc, descWidth), Reset+nl)
		}
	}
}

// harnessLabel is the short name a harness goes by in the UI.
func harnessLabel(h session.Harness) string {
	switch h {
	case session.HarnessClaude:
		return "cc"
	case session.HarnessOMP:
		return "omp"
	default:
		return string(h)
	}
}

// formatProject formats the project name with optional indicators, padded to maxLen visible chars
func formatProject(s session.Session, maxLen int, showHarness bool) string {
	// Sanitize to prevent ANSI escape injection from log/filesystem content
	name := sanitizeForTerminal(s.Project)
	var suffixes []string
	var suffixLens []int // visible length of each suffix (excluding space)

	// Which agent this row belongs to, first so the width-shedding loop below
	// drops it last: on a mixed dashboard, knowing which agent a row is matters
	// more than its branch or title.
	if showHarness && s.Harness != "" {
		label := harnessLabel(s.Harness)
		suffixes = append(suffixes, Dim+"["+label+"]"+Reset)
		suffixLens = append(suffixLens, 2+len([]rune(label))) // [label]
	}

	// Add git branch if present (show first, most useful)
	if s.GitBranch != "" {
		branch := sanitizeForTerminal(s.GitBranch)
		branchRunes := []rune(branch)
		if len(branchRunes) > 12 {
			branchRunes = branchRunes[:12]
			branch = string(branchRunes)
		}
		suffixes = append(suffixes, Dim+"@"+branch+Reset)
		suffixLens = append(suffixLens, 1+len(branchRunes)) // @branch (visible rune count)
	}

	// Add session title if present
	if s.SessionTitle != "" {
		title := sanitizeForTerminal(s.SessionTitle)
		titleRunes := []rune(title)
		if len(titleRunes) > 20 {
			titleRunes = titleRunes[:20]
			title = string(titleRunes)
		}
		suffixes = append(suffixes, Dim+"\""+title+"\""+Reset)
		suffixLens = append(suffixLens, 2+len(titleRunes)) // "title" (visible rune count)
	}

	// Ghost indicator (highest priority warning)
	if s.IsGhost {
		suffixes = append(suffixes, Red+"[ghost]"+Reset)
		suffixLens = append(suffixLens, 7) // [ghost]
	}

	// Incomplete data warning: this row's numbers are partly missing, so they
	// must not read as measurements.
	if s.Degraded != "" {
		suffixes = append(suffixes, Yellow+"[?]"+Reset)
		suffixLens = append(suffixLens, 3) // [?]
	}

	// Unsandboxed indicator (security warning)
	if s.HasUnsandboxed {
		suffixes = append(suffixes, Yellow+"[!S]"+Reset)
		suffixLens = append(suffixLens, 4) // [!S]
	}

	// Drop suffixes from the end until they fit, keeping at least 4 chars for the name
	const minNameWidth = 4
	totalSuffixLen := 0
	for _, l := range suffixLens {
		totalSuffixLen += 1 + l // space + indicator
	}
	for len(suffixes) > 0 && maxLen-totalSuffixLen < minNameWidth {
		last := len(suffixLens) - 1
		totalSuffixLen -= 1 + suffixLens[last]
		suffixes = suffixes[:last]
		suffixLens = suffixLens[:last]
	}

	// Truncate name to fit
	nameWidth := maxLen - totalSuffixLen
	if nameWidth < 1 {
		nameWidth = 1
	}
	truncated := truncate(name, nameWidth)
	// Runes, not bytes: truncate cut by rune, and padding to a byte count makes
	// every column right of a non-ASCII project name shift left.
	visibleLen := len([]rune(truncated))

	// Build result
	result := truncated
	for i, suffix := range suffixes {
		result += " " + suffix
		visibleLen += 1 + suffixLens[i] // space + indicator visible length
	}

	// Pad to maxLen with spaces (ANSI codes don't count for visual width)
	if visibleLen < maxLen {
		result += strings.Repeat(" ", maxLen-visibleLen)
	}

	return result
}
