package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// RenderHistory renders the session history view with date grouping
// When showFooter is true, uses \r\n for raw terminal mode
func RenderHistory(sessions []session.HistorySession, days int, showFooter bool) {
	// Use \r\n when in interactive mode (showFooter=true means raw terminal)
	nl := "\n"
	if showFooter {
		nl = "\r\n"
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

	// Header
	fmt.Printf("%sSession History%s (past %d days)%s%s", Bold, Reset, days, nl, nl)

	// Column headers (once at the top)
	colHeader := fmt.Sprintf("%-*s %-*s %-*s %-*s %*s",
		l.project, "PROJECT",
		l.branch, "BRANCH",
		l.startTime, "TIME",
		l.duration, "DURATION",
		l.msgs, "MSGS")
	fmt.Print(colHeader + nl)

	// Group sessions by date
	var currentGroup string
	var totalDuration time.Duration
	totalSessions := 0
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
			fmt.Printf("%s━━━ %s %s%s%s", Dim, group, strings.Repeat("━", separatorLen), Reset, nl)
			currentGroup = group
			rowsUsed++
		}

		// Format start time
		startTime := s.StartTime.Format("15:04")

		// Format duration
		duration := formatDuration(s.Duration)

		row := fmt.Sprintf("%-*s %s%-*s%s %-*s %-*s %*d",
			l.project, truncate(s.Project, l.project),
			Gray, l.branch, truncate(s.GitBranch, l.branch), Reset,
			l.startTime, startTime,
			l.duration, duration,
			l.msgs, s.MessageCount)
		fmt.Print(row + nl)
		rowsUsed++

		totalDuration += s.Duration
		totalSessions++
	}

	// Truncation indicator
	if truncated > 0 {
		fmt.Printf("%s  ... and %d more sessions%s%s", Dim, truncated, Reset, nl)
	}

	// Footer with totals
	fmt.Printf("%s%s%s%s%s", nl, Dim, strings.Repeat("─", l.totalWidth), Reset, nl)
	fmt.Printf("%sTotal: %d sessions, %s%s%s", Dim, totalSessions, formatDuration(totalDuration), Reset, nl)

	if showFooter {
		fmt.Printf("%s%sl: live view | u: usage | Ctrl+C: quit%s%s", nl, Dim, Reset, nl)
	}
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
