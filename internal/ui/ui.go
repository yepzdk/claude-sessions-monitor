package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
)

// ANSI color codes
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Gray    = "\033[90m"
	BgGreen = "\033[42m"
)

// Status symbols
const (
	SymbolWorking    = "●"
	SymbolNeedsInput = "⚠"
	SymbolWaiting    = "◉"
	SymbolIdle       = "○"
)

// RenderList renders sessions as a simple list (for -l flag)
func RenderList(sessions []session.Session) {
	if len(sessions) == 0 {
		fmt.Println("No active Claude sessions found.")
		return
	}

	// Header
	fmt.Printf("%-15s %-35s %-15s %s\n", "STATUS", "PROJECT", "LAST ACTIVITY", "TASK")
	fmt.Println(strings.Repeat("─", 80))

	for _, s := range sessions {
		symbol, color := getStatusDisplay(s.Status)
		elapsed := formatElapsed(time.Since(s.LastActivity))

		fmt.Printf("%s%s %-13s%s %-35s %-15s %s\n",
			color, symbol, s.Status, Reset,
			truncate(s.Project, 35),
			elapsed,
			truncate(s.Task, 30))
	}
}

// RenderJSON renders sessions as JSON
func RenderJSON(sessions []session.Session) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(sessions)
}

// RenderLive renders the live dashboard view
func RenderLive(sessions []session.Session) {
	// Clear screen and move cursor to top
	fmt.Print("\033[2J\033[H")

	// Header
	fmt.Printf("%s🤖 Claude Code Sessions%s\n\n", Bold, Reset)

	// Status summary
	counts := countByStatus(sessions)
	fmt.Printf("%s%s Working: %d%s  ", Green, SymbolWorking, counts[session.StatusWorking], Reset)
	fmt.Printf("%s%s Needs Input: %d%s  ", Yellow, SymbolNeedsInput, counts[session.StatusNeedsInput], Reset)
	fmt.Printf("%s%s Waiting: %d%s  ", Blue, SymbolWaiting, counts[session.StatusWaiting], Reset)
	fmt.Printf("%s%s Idle: %d%s\n\n", Gray, SymbolIdle, counts[session.StatusIdle], Reset)

	if len(sessions) == 0 {
		fmt.Printf("%sNo active Claude sessions found.%s\n", Dim, Reset)
	} else {
		// Column headers
		fmt.Printf("  %-15s %-35s %-15s %s\n", "STATUS", "PROJECT", "LAST ACTIVITY", "CURRENT TASK")
		fmt.Printf("  %s\n", strings.Repeat("─", 78))

		for _, s := range sessions {
			symbol, color := getStatusDisplay(s.Status)
			elapsed := formatElapsed(time.Since(s.LastActivity))

			fmt.Printf("  %s%s %-13s%s %-35s %-15s %s\n",
				color, symbol, s.Status, Reset,
				truncate(s.Project, 35),
				elapsed,
				truncate(s.Task, 25))
		}
	}

	fmt.Printf("\n  %sPress Ctrl+C to quit%s\n", Dim, Reset)
}

// ClearScreen clears the terminal screen
func ClearScreen() {
	fmt.Print("\033[2J\033[H")
}

// HideCursor hides the terminal cursor
func HideCursor() {
	fmt.Print("\033[?25l")
}

// ShowCursor shows the terminal cursor
func ShowCursor() {
	fmt.Print("\033[?25h")
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
	case session.StatusIdle:
		return SymbolIdle, Gray
	default:
		return SymbolIdle, Reset
	}
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

// truncate truncates a string to a maximum length
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
