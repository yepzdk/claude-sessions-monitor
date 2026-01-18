package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// RenderHistory renders the session history view with date grouping
func RenderHistory(sessions []session.HistorySession, days int, showFooter bool) {
	if len(sessions) == 0 {
		fmt.Printf("No sessions found in the past %d days.\n", days)
		return
	}

	// Header
	fmt.Printf("%sSession History%s (past %d days)\n\n", Bold, Reset, days)

	// Group sessions by date
	var currentGroup string
	var totalDuration time.Duration
	totalSessions := 0

	for _, s := range sessions {
		group := session.GetDateGroup(s.StartTime)

		// Print date header when group changes
		if group != currentGroup {
			if currentGroup != "" {
				fmt.Println() // Empty line between groups
			}
			fmt.Printf("%s━━━ %s %s%s\n", Dim, group, strings.Repeat("━", 60-len(group)), Reset)
			fmt.Printf("%-27s %-10s %-10s %-6s %s\n", "PROJECT", "BRANCH", "DURATION", "MSGS", "CONTEXT")
			currentGroup = group
		}

		// Format duration
		duration := formatDuration(s.Duration)

		// Format context (first prompt, truncated)
		context := s.FirstPrompt
		if context == "" {
			context = "-"
		}

		fmt.Printf("%-27s %s%-10s%s %-10s %-6d %s\n",
			truncate(s.Project, 27),
			Gray, truncate(s.GitBranch, 10), Reset,
			duration,
			s.MessageCount,
			truncate(context, 35))

		totalDuration += s.Duration
		totalSessions++
	}

	// Footer with totals
	fmt.Printf("\n%s%s%s\n", Dim, strings.Repeat("─", 70), Reset)
	fmt.Printf("%sTotal: %d sessions, %s%s\n", Dim, totalSessions, formatDuration(totalDuration), Reset)

	if showFooter {
		fmt.Printf("\n%sPress l: live view | Ctrl+C: quit%s\n", Dim, Reset)
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
