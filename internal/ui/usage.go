package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// usageBarWidth is the number of block characters in the usage progress bar
const usageBarWidth = 20

// RenderUsage renders the token usage view in the terminal.
// Uses \r\n for newlines when in raw terminal mode (showFooter=true).
func RenderUsage(usage *session.UsageStats, apiQuota *session.APIQuota, showFooter bool) {
	nl := "\n"
	if showFooter {
		nl = "\r\n"
	}

	fmt.Printf("%sToken Usage%s%s%s", Bold, Reset, nl, nl)

	// --- API Quota Section ---
	width := getTerminalWidth()
	sectionHeader := "API Quota"
	separatorLen := width - 4 - len(sectionHeader) - 1
	if separatorLen < 1 {
		separatorLen = 1
	}
	fmt.Printf("%s━━━ %s %s%s%s", Dim, sectionHeader, strings.Repeat("━", separatorLen), Reset, nl)

	if apiQuota != nil && apiQuota.Available {
		renderQuotaBucket("5-hour", apiQuota.FiveHour, nl)
		renderQuotaBucket("7-day", apiQuota.SevenDay, nl)
		if apiQuota.SevenDaySonnet != nil {
			renderQuotaBucket("Sonnet", apiQuota.SevenDaySonnet, nl)
		}
		if apiQuota.SevenDayOpus != nil {
			renderQuotaBucket("Opus", apiQuota.SevenDayOpus, nl)
		}
		if apiQuota.ExtraUsage != nil && apiQuota.ExtraUsage.IsEnabled {
			fmt.Printf("  %sExtra usage: enabled%s%s", Dim, Reset, nl)
		}
	} else {
		errMsg := "OAuth token not found"
		if apiQuota != nil && apiQuota.Error != "" {
			errMsg = apiQuota.Error
		}
		fmt.Printf("  %sNot available (%s)%s%s", Dim, errMsg, Reset, nl)
	}

	fmt.Print(nl)

	// --- Local Usage Section ---
	sectionHeader = "Local Usage (5h window)"
	separatorLen = width - 4 - len(sectionHeader) - 1
	if separatorLen < 1 {
		separatorLen = 1
	}
	fmt.Printf("%s━━━ %s %s%s%s", Dim, sectionHeader, strings.Repeat("━", separatorLen), Reset, nl)

	if usage != nil && usage.TotalTokens > 0 {
		fmt.Printf("  Total tokens:  %s (input: %s | output: %s | cache: %s)%s",
			formatTokenCount(usage.TotalTokens),
			formatTokenCount(usage.InputTokens),
			formatTokenCount(usage.OutputTokens),
			formatTokenCount(usage.CacheTokens),
			nl)
		fmt.Printf("  Sessions:      %d%s", len(usage.Sessions), nl)
		fmt.Print(nl)

		// Per-session table
		l := calcUsageLayout(width)
		header := fmt.Sprintf("  %-*s %*s %*s %*s %*s",
			l.project, "PROJECT",
			l.input, "INPUT",
			l.output, "OUTPUT",
			l.cache, "CACHE",
			l.total, "TOTAL")
		fmt.Print(header + nl)
		fmt.Printf("  %s%s", strings.Repeat("─", l.totalWidth), nl)

		for _, su := range usage.Sessions {
			project := truncate(su.Project, l.project)
			row := fmt.Sprintf("  %-*s %*s %*s %*s %*s",
				l.project, project,
				l.input, formatTokenCount(su.InputTokens),
				l.output, formatTokenCount(su.OutputTokens),
				l.cache, formatTokenCount(su.CacheTokens),
				l.total, formatTokenCount(su.TotalTokens))
			fmt.Print(row + nl)
		}
	} else {
		fmt.Printf("  %sNo token usage in the past 5 hours.%s%s", Dim, Reset, nl)
	}

	// Footer
	if showFooter {
		fmt.Printf("%s%sl: live | h: history | Ctrl+C: quit%s%s", nl, Dim, Reset, nl)
	}
}

// renderQuotaBucket renders a single quota bucket bar line.
func renderQuotaBucket(label string, bucket *session.QuotaBucket, nl string) {
	if bucket == nil {
		return
	}

	pct := bucket.Utilization
	if pct > 100 {
		pct = 100
	}

	filled := int(pct / 100 * float64(usageBarWidth))
	if filled > usageBarWidth {
		filled = usageBarWidth
	}
	empty := usageBarWidth - filled

	var color string
	switch {
	case pct >= 90:
		color = Red
	case pct >= 75:
		color = Yellow
	default:
		color = Green
	}

	bar := color + strings.Repeat("█", filled) + Reset +
		Dim + strings.Repeat("░", empty) + Reset

	resetStr := ""
	if bucket.ResetsAt != nil {
		remaining := time.Until(*bucket.ResetsAt)
		if remaining > 0 {
			resetStr = fmt.Sprintf("   resets in %s", formatDurationCompact(remaining))
		}
	}

	fmt.Printf("  %-8s %s %3.0f%%%s%s%s%s", label, bar, pct, Dim, resetStr, Reset+nl, nl)
}

// formatTokenCount formats a token count as a human-readable string (e.g. "2.1M", "150K")
func formatTokenCount(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.0fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatDurationCompact formats a duration as a compact human-readable string
func formatDurationCompact(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
