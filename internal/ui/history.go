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
		// Reserve: header (2) + footer totals (3: blank+separator+total) + help (2: blank+help)
		reserved := 7
		maxRows = height - reserved
		if maxRows < 3 {
			maxRows = 3
		}
	}

	// Header
	fmt.Printf("%sSession History%s (past %d days)%s%s", Bold, Reset, days, nl, nl)

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
			rowsNeeded += 2 // group separator + column header
			if currentGroup != "" {
				rowsNeeded++ // blank line between groups
			}
		}

		// Check if we'd exceed the budget
		if maxRows > 0 && rowsUsed+rowsNeeded > maxRows {
			truncated = len(sessions) - totalSessions
			break
		}

		// Print date header when group changes
		if group != currentGroup {
			if currentGroup != "" {
				fmt.Print(nl) // Empty line between groups
				rowsUsed++
			}
			separatorLen := l.totalWidth - 5 - len(group) // "━━━ " (4) + " " after group (1)
			if separatorLen < 1 {
				separatorLen = 1
			}
			fmt.Printf("%s━━━ %s %s%s%s", Dim, group, strings.Repeat("━", separatorLen), Reset, nl)

			colHeader := fmt.Sprintf("%-*s %-*s %-*s %-*s",
				l.project, "PROJECT",
				l.branch, "BRANCH",
				l.duration, "DURATION",
				l.msgs, "MSGS")
			if l.showContext {
				colHeader += fmt.Sprintf(" %s", "CONTEXT")
			}
			fmt.Print(colHeader + nl)
			currentGroup = group
			rowsUsed += 2
		}

		// Format duration
		duration := formatDuration(s.Duration)

		// Format context (first prompt, truncated)
		context := s.FirstPrompt
		if context == "" {
			context = "-"
		}

		row := fmt.Sprintf("%-*s %s%-*s%s %-*s %-*d",
			l.project, truncate(s.Project, l.project),
			Gray, l.branch, truncate(s.GitBranch, l.branch), Reset,
			l.duration, duration,
			l.msgs, s.MessageCount)
		if l.showContext {
			row += " " + truncate(context, l.context-1)
		}
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
		fmt.Printf("%s%sl: live view | Ctrl+C: quit%s%s", nl, Dim, Reset, nl)
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
