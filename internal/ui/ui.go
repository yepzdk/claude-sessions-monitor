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
	SymbolInactive   = "◌"
)

// RenderList renders sessions as a simple list (for -l flag)
func RenderList(sessions []session.Session) {
	if len(sessions) == 0 {
		fmt.Println("No active Claude sessions found.")
		return
	}

	// Header
	fmt.Printf("%-15s %-35s %-15s %s\n", "STATUS", "PROJECT", "LAST ACTIVITY", "LAST MESSAGE")
	fmt.Println(strings.Repeat("─", 100))

	for _, s := range sessions {
		symbol, color := getStatusDisplay(s.Status)
		elapsed := formatElapsed(time.Since(s.LastActivity))

		// Use last message if available, otherwise task
		desc := s.LastMessage
		if desc == "" {
			desc = s.Task
		}

		fmt.Printf("%s%s %-13s%s %-35s %-15s %s\n",
			color, symbol, s.Status, Reset,
			truncate(s.Project, 35),
			elapsed,
			truncate(desc, 40))
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
	// Set terminal title with status summary
	SetTerminalTitle(buildTerminalTitle(sessions))

	// Clear screen and move cursor to top
	fmt.Print("\033[2J\033[H")

	// Header
	fmt.Printf("%sClaude Code Sessions%s\n\n", Bold, Reset)

	// Split sessions into active and inactive
	var active, inactive []session.Session
	for _, s := range sessions {
		if s.Status == session.StatusInactive {
			inactive = append(inactive, s)
		} else {
			active = append(active, s)
		}
	}

	// Status summary (only active sessions)
	counts := countByStatus(active)
	fmt.Printf("%s%s Working: %d%s  ", Green, SymbolWorking, counts[session.StatusWorking], Reset)
	fmt.Printf("%s%s Needs Input: %d%s  ", Yellow, SymbolNeedsInput, counts[session.StatusNeedsInput], Reset)
	fmt.Printf("%s%s Waiting: %d%s  ", Blue, SymbolWaiting, counts[session.StatusWaiting], Reset)
	fmt.Printf("%s%s Idle: %d%s  ", Gray, SymbolIdle, counts[session.StatusIdle], Reset)
	fmt.Printf("%s%s Inactive: %d%s\n\n", Dim, SymbolInactive, len(inactive), Reset)

	if len(active) == 0 {
		fmt.Printf("%sNo active Claude sessions.%s\n", Dim, Reset)
	} else {
		// Column headers
		fmt.Printf("%-15s %-35s %-15s %s\n", "STATUS", "PROJECT", "LAST ACTIVITY", "LAST MESSAGE")
		fmt.Printf("%s\n", strings.Repeat("─", 95))

		for _, s := range active {
			symbol, color := getStatusDisplay(s.Status)
			elapsed := formatElapsed(time.Since(s.LastActivity))

			// Use last message if available, otherwise task
			desc := s.LastMessage
			if desc == "" {
				desc = s.Task
			}

			fmt.Printf("%s%s %-13s%s %-35s %-15s %s\n",
				color, symbol, s.Status, Reset,
				truncate(s.Project, 35),
				elapsed,
				truncate(desc, 35))
		}
	}

	fmt.Printf("\n%sPress Ctrl+C to quit%s\n", Dim, Reset)
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

// SetTerminalTitle sets the terminal tab/window title
func SetTerminalTitle(title string) {
	fmt.Printf("\033]0;%s\007", title)
}

// ResetTerminalTitle resets the terminal title to default
func ResetTerminalTitle() {
	fmt.Print("\033]0;\007")
}

// buildTerminalTitle creates a status summary for the terminal title
func buildTerminalTitle(sessions []session.Session) string {
	counts := make(map[session.Status]int)
	for _, s := range sessions {
		if s.Status != session.StatusInactive {
			counts[s.Status]++
		}
	}

	// Priority: Needs Input > Working > Waiting > Idle
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
	if n := counts[session.StatusIdle]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d idle", n))
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
	case session.StatusIdle:
		return SymbolIdle, Gray
	case session.StatusInactive:
		return SymbolInactive, Dim
	default:
		return SymbolInactive, Reset
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
