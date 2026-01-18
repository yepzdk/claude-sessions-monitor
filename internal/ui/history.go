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

	// Header
	fmt.Printf("%sSession History%s (past %d days)%s%s", Bold, Reset, days, nl, nl)

	// Group sessions by date
	var currentGroup string
	var totalDuration time.Duration
	totalSessions := 0

	for _, s := range sessions {
		group := session.GetDateGroup(s.StartTime)

		// Print date header when group changes
		if group != currentGroup {
			if currentGroup != "" {
				fmt.Print(nl) // Empty line between groups
			}
			fmt.Printf("%s━━━ %s %s%s%s", Dim, group, strings.Repeat("━", 60-len(group)), Reset, nl)
			fmt.Printf("%-27s %-10s %-10s %-6s %s%s", "PROJECT", "BRANCH", "DURATION", "MSGS", "CONTEXT", nl)
			currentGroup = group
		}

		// Format duration
		duration := formatDuration(s.Duration)

		// Format context (first prompt, truncated)
		context := s.FirstPrompt
		if context == "" {
			context = "-"
		}

		fmt.Printf("%-27s %s%-10s%s %-10s %-6d %s%s",
			truncate(s.Project, 27),
			Gray, truncate(s.GitBranch, 10), Reset,
			duration,
			s.MessageCount,
			truncate(context, 35),
			nl)

		totalDuration += s.Duration
		totalSessions++
	}

	// Footer with totals
	fmt.Printf("%s%s%s%s%s", nl, Dim, strings.Repeat("─", 70), Reset, nl)
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
