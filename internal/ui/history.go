package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// RenderHistory renders the session history view with date grouping
// When showFooter is true, uses \r\n for raw terminal mode
// errMsg, when non-empty, explains why the list below may be incomplete.
// "No sessions found" is a claim about the past; it must not be printed when
// the reason for the empty list is that the search itself failed.
func RenderHistory(sessions []session.HistorySession, days int, showFooter bool, errMsg string) {
	nl := newlineFor(showFooter)

	if errMsg != "" {
		fmt.Printf("%sCannot read session history: %s%s%s",
			Red, sanitizeForTerminal(errMsg), Reset, nl)
		if len(sessions) == 0 {
			return
		}
	}

	if len(sessions) == 0 {
		fmt.Printf("No sessions found in the past %d days.%s", days, nl)
		return
	}

	l := calcHistoryLayout(getTerminalWidth())

	// Calculate row budget when in interactive mode
	maxRows := 0 // 0 = unlimited (non-interactive)
	if showFooter {
		height := getTerminalHeight()
		// Reserve: header (2) + column header (1) + footer totals (3: blank+separator+total) + help (2: blank+help)
		reserved := 8
		maxRows = height - reserved
		if maxRows < 3 {
			maxRows = 3
		}
	}

	// Build the whole frame in memory and write it out in a single syscall —
	// see rawNewline in ui.go for why printing line-by-line causes flicker.
	var buf strings.Builder

	// Header
	fmt.Fprintf(&buf, "%sSession History%s (past %d days)%s%s", Bold, Reset, days, nl, nl)

	// Column headers (once at the top)
	colHeader := fmt.Sprintf("%-*s %-*s %-*s %-*s %*s",
		l.project, "PROJECT",
		l.branch, "BRANCH",
		l.startTime, "TIME",
		l.duration, "DURATION",
		l.msgs, "MSGS")
	buf.WriteString(colHeader + nl)

	// Group sessions by date
	var currentGroup string
	var totalDuration time.Duration
	totalSessions := 0
	degradedRows := 0
	rowsUsed := 0
	truncated := 0

	for _, s := range sessions {
		group := session.GetDateGroup(s.StartTime)

		// Calculate how many rows this entry needs
		rowsNeeded := 1 // the session row itself
		if group != currentGroup {
			rowsNeeded++ // group separator line
		}

		// Check if we'd exceed the budget
		if maxRows > 0 && rowsUsed+rowsNeeded > maxRows {
			truncated = len(sessions) - totalSessions
			break
		}

		// Print date separator when group changes
		if group != currentGroup {
			separatorLen := l.totalWidth - 5 - len(group) // "━━━ " (4) + " " after group (1)
			if separatorLen < 1 {
				separatorLen = 1
			}
			fmt.Fprintf(&buf, "%s━━━ %s %s%s%s", Dim, group, strings.Repeat("━", separatorLen), Reset, nl)
			currentGroup = group
			rowsUsed++
		}

		// Format start time
		startTime := s.StartTime.Format("15:04")

		// Format duration
		duration := formatDuration(s.Duration)

		row := fmt.Sprintf("%s %s%-*s%s %-*s %-*s %*d",
			historyProjectCell(s, l.project),
			Gray, l.branch, truncate(s.GitBranch, l.branch), Reset,
			l.startTime, startTime,
			l.duration, duration,
			l.msgs, s.MessageCount)
		buf.WriteString(row + nl)
		rowsUsed++

		totalDuration += s.Duration
		totalSessions++
		if s.Degraded != "" {
			degradedRows++
		}
	}

	// Truncation indicator
	if truncated > 0 {
		fmt.Fprintf(&buf, "%s  ... and %d more sessions%s%s", Dim, truncated, Reset, nl)
	}

	// Footer with totals
	fmt.Fprintf(&buf, "%s%s%s%s%s", nl, Dim, strings.Repeat("─", l.totalWidth), Reset, nl)
	fmt.Fprintf(&buf, "%sTotal: %d sessions, %s%s%s", Dim, totalSessions, formatDuration(totalDuration), Reset, nl)
	if degradedRows > 0 {
		fmt.Fprintf(&buf, "%s! %d session(s) could not be read in full; the total is a lower bound.%s%s",
			Yellow, degradedRows, Reset, nl)
	}

	if showFooter {
		fmt.Fprintf(&buf, "%s%sl: live view | u: usage | Ctrl+C: quit%s%s", nl, Dim, Reset, nl)
	}

	fmt.Print(buf.String())
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

// historyProjectCell renders the project column, marking a row whose log could
// not be read to the end. Its duration and message count are a floor, and
// without the marker a session someone spent an afternoon in reads as "0s, 0".
//
// The cell is padded here rather than by the caller's %-*s because the marker
// carries colour codes, which pad as though they were visible characters.
func historyProjectCell(s session.HistorySession, width int) string {
	if s.Degraded == "" {
		return fmt.Sprintf("%-*s", width, truncate(s.Project, width))
	}

	const marker = " [?]"
	name := truncate(s.Project, width-len(marker))
	pad := width - utf8.RuneCountInString(name) - len(marker)
	if pad < 0 {
		pad = 0
	}
	return name + strings.Repeat(" ", pad) + " " + Yellow + "[?]" + Reset
}
