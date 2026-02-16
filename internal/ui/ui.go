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
	Red     = "\033[31m"
	Gray    = "\033[90m"
	BgGreen = "\033[42m"
)

// Status symbols (all narrow/single-column width for consistent alignment)
const (
	SymbolWorking    = "●"
	SymbolNeedsInput = "▲"
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
	fmt.Printf("%-15s %-35s %-16s %-15s %s\n", "STATUS", "PROJECT", "CONTEXT", "LAST ACTIVITY", "LAST MESSAGE")
	fmt.Println(strings.Repeat("─", 115))

	for _, s := range sessions {
		elapsed := formatElapsed(time.Since(s.LastActivity))

		// Use last message if available, otherwise task
		desc := s.LastMessage
		if desc == "" {
			desc = s.Task
		}

		fmt.Printf("%s %s %s %-15s %s\n",
			formatStatus(s.Status, 15),
			formatProject(s, 35),
			formatContext(s, 16),
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
// Uses \r\n for newlines to work correctly in raw terminal mode
func RenderLive(sessions []session.Session) {
	// Set terminal title with status summary
	SetTerminalTitle(buildTerminalTitle(sessions))

	// Clear screen and move cursor to top
	fmt.Print("\033[2J\033[H")

	// Header
	fmt.Printf("%sClaude Code Sessions%s\r\n\r\n", Bold, Reset)

	// Split sessions into active and inactive (ghosts are included in inactive)
	var active, inactive []session.Session
	for _, s := range sessions {
		if s.IsGhost || s.Status == session.StatusInactive {
			inactive = append(inactive, s)
		} else {
			active = append(active, s)
		}
	}

	// Status summary (only active sessions)
	counts := countByStatus(active)
	fmt.Printf("%s%s Working: %d%s  ", Green, SymbolWorking, counts[session.StatusWorking], Reset)
	fmt.Printf("%s%s Needs Input: %d%s  ", Yellow, SymbolNeedsInput, counts[session.StatusNeedsInput], Reset)
	fmt.Printf("%s%s Waiting: %d%s", Blue, SymbolWaiting, counts[session.StatusWaiting], Reset)
	fmt.Print("\r\n\r\n")

	if len(active) == 0 {
		fmt.Printf("%sNo active Claude sessions.%s\r\n", Dim, Reset)
	} else {
		// Column headers
		fmt.Printf("%-15s %-35s %-16s %-15s %s\r\n", "STATUS", "PROJECT", "CONTEXT", "LAST ACTIVITY", "LAST MESSAGE")
		fmt.Printf("%s\r\n", strings.Repeat("─", 115))

		for i, s := range active {
			elapsed := formatElapsed(time.Since(s.LastActivity))

			// Use last message if available, otherwise task
			desc := s.LastMessage
			if desc == "" {
				desc = s.Task
			}

			fmt.Printf("%s %s %s %-15s %s\r\n",
				formatStatus(s.Status, 15),
				formatProject(s, 35),
				formatContext(s, 16),
				elapsed,
				truncate(desc, 35))

			// Add spacing between rows so progress bars don't merge visually
			if i < len(active)-1 {
				fmt.Print("\r\n")
			}
		}
	}

	// Show help footer
	fmt.Printf("\r\n%sh: history | Ctrl+C: quit%s\r\n", Dim, Reset)
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

// ResetTerminalTitle resets the terminal title to default
func ResetTerminalTitle() {
	fmt.Print("\033]0;\007")
}

// buildTerminalTitle creates a status summary for the terminal title
func buildTerminalTitle(sessions []session.Session) string {
	counts := make(map[session.Status]int)
	for _, s := range sessions {
		if s.Status != session.StatusInactive && !s.IsGhost {
			counts[s.Status]++
		}
	}

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
	case session.StatusIdle:
		return SymbolIdle, Gray
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
	bar := color + strings.Repeat("█", filled) + Reset +
		Dim + strings.Repeat("░", empty) + Reset +
		label

	// Pad to width (visible length = bar chars + label chars)
	visibleLen := contextBarWidth + len(label)
	if visibleLen < width {
		bar += strings.Repeat(" ", width-visibleLen)
	}

	return bar
}

// formatProject formats the project name with optional indicators, padded to maxLen visible chars
func formatProject(s session.Session, maxLen int) string {
	name := s.Project
	var suffixes []string
	var suffixLens []int // visible length of each suffix (excluding space)

	// Add git branch if present (show first, most useful)
	if s.GitBranch != "" {
		branch := s.GitBranch
		if len(branch) > 12 {
			branch = branch[:12]
		}
		suffixes = append(suffixes, Dim+"@"+branch+Reset)
		suffixLens = append(suffixLens, 1+len(branch)) // @branch
	}

	// Ghost indicator (highest priority warning)
	if s.IsGhost {
		suffixes = append(suffixes, Red+"[ghost]"+Reset)
		suffixLens = append(suffixLens, 7) // [ghost]
	}

	// Unsandboxed indicator (security warning)
	if s.HasUnsandboxed {
		suffixes = append(suffixes, Yellow+"[!S]"+Reset)
		suffixLens = append(suffixLens, 4) // [!S]
	}

	// Desktop indicator (lowest priority)
	if s.IsDesktop {
		suffixes = append(suffixes, Dim+"[D]"+Reset)
		suffixLens = append(suffixLens, 3) // [D]
	}

	// Calculate total suffix visible length (indicators + spaces)
	totalSuffixLen := 0
	for _, l := range suffixLens {
		totalSuffixLen += 1 + l // space + indicator
	}

	// Truncate name to fit
	truncated := truncate(name, maxLen-totalSuffixLen)
	visibleLen := len(truncated)

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
