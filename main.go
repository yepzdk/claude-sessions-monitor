package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
	"github.com/itk-dev/claude-sessions-monitor/internal/ui"
)

var version = "dev"

func main() {
	// Parse flags
	listOnce := flag.Bool("l", false, "List sessions once and exit")
	jsonOutput := flag.Bool("json", false, "Output as JSON (requires -l)")
	showVersion := flag.Bool("v", false, "Show version")
	interval := flag.Duration("interval", 2*time.Second, "Refresh interval for live view")
	historyMode := flag.Bool("history", false, "Show session history")
	historyDays := flag.Int("days", 7, "Number of days for history (default 7)")
	killGhosts := flag.Bool("kill-ghosts", false, "Find and terminate ghost (orphaned) Claude processes")
	flag.Parse()

	// Handle version
	if *showVersion {
		fmt.Printf("csm version %s\n", version)
		os.Exit(0)
	}

	// Handle kill-ghosts mode
	if *killGhosts {
		handleKillGhosts()
		return
	}

	// Handle history mode
	if *historyMode {
		sessions, err := session.DiscoverHistory(*historyDays)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering history: %v\n", err)
			os.Exit(1)
		}
		ui.RenderHistory(sessions, *historyDays, false)
		return
	}

	// Handle list mode
	if *listOnce {
		sessions, err := session.Discover()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering sessions: %v\n", err)
			os.Exit(1)
		}

		if *jsonOutput {
			if err := ui.RenderJSON(sessions); err != nil {
				fmt.Fprintf(os.Stderr, "Error rendering JSON: %v\n", err)
				os.Exit(1)
			}
		} else {
			ui.RenderList(sessions)
		}
		return
	}

	// Live view mode
	runLiveView(*interval)
}

// ViewMode represents the current display mode
type ViewMode int

const (
	ViewModeLive ViewMode = iota
	ViewModeHistory
)

func runLiveView(interval time.Duration) {
	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Set up keyboard input
	if err := ui.SetupRawInput(); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up keyboard input: %v\n", err)
		os.Exit(1)
	}

	// Start keyboard reader
	keyCh := make(chan rune, 1)
	done := make(chan struct{})
	go ui.ReadKey(keyCh, done)

	// Track current view mode
	viewMode := ViewModeLive
	historyDays := 7

	// Hide cursor and ensure cleanup on exit
	ui.HideCursor()
	defer func() {
		close(done)
		ui.CleanupRawInput()
		ui.ShowCursor()
		ui.ResetTerminalTitle()
		ui.ClearScreen()
		fmt.Println("Goodbye!")
	}()

	// Render function that respects current mode
	render := func() {
		if viewMode == ViewModeHistory {
			ui.ClearScreen()
			sessions, _ := session.DiscoverHistory(historyDays)
			ui.RenderHistory(sessions, historyDays, true)
		} else {
			sessions, _ := session.Discover()
			ui.RenderLive(sessions)
		}
	}

	// Initial render
	render()

	// Main loop with both watcher and keyboard input
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			cancel()
			return
		case <-ctx.Done():
			return
		case key := <-keyCh:
			switch key {
			case 'h', 'H':
				if viewMode != ViewModeHistory {
					viewMode = ViewModeHistory
					render()
				}
			case 'l', 'L':
				if viewMode != ViewModeLive {
					viewMode = ViewModeLive
					render()
				}
			case 3: // Ctrl+C
				cancel()
				return
			}
		case <-ticker.C:
			render()
		}
	}
}

// handleKillGhosts finds and terminates ghost Claude processes
func handleKillGhosts() {
	ghosts, err := session.FindGhostProcesses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding ghost processes: %v\n", err)
		os.Exit(1)
	}

	if len(ghosts) == 0 {
		fmt.Println("No ghost processes found.")
		return
	}

	fmt.Printf("Found %d ghost process(es):\n\n", len(ghosts))
	for _, g := range ghosts {
		fmt.Printf("  PID %d - %s (inactive for %s)\n", g.PID, g.Project, session.FormatAge(g.Age))
	}
	fmt.Println()

	killed, err := session.KillGhostProcesses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error killing ghost processes: %v\n", err)
		os.Exit(1)
	}

	if len(killed) == 0 {
		fmt.Println("No processes were terminated (they may have already exited).")
	} else {
		fmt.Printf("Terminated %d ghost process(es).\n", len(killed))
	}
}
