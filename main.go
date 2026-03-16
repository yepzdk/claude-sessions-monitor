package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/itk-dev/claude-sessions-monitor/internal/session"
	"github.com/itk-dev/claude-sessions-monitor/internal/ui"
	"github.com/itk-dev/claude-sessions-monitor/internal/web"
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
	webMode := flag.Bool("web", false, "Start web dashboard server")
	webOnly := flag.Bool("web-only", false, "Start web dashboard server without terminal UI (headless)")
	webPort := flag.Int("port", 9847, "Port for web dashboard (default 9847)")
	flag.Parse()

	// Check for conflicting flags
	if *webMode && *webOnly {
		fmt.Fprintf(os.Stderr, "Error: --web and --web-only are mutually exclusive\n")
		os.Exit(1)
	}

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

	// Headless web-only mode (no terminal UI)
	if *webOnly {
		runWebOnly(*webPort)
		return
	}

	// Live view mode
	runLiveView(*interval, *webMode, *webPort)
}

// ViewMode represents the current display mode
type ViewMode int

const (
	ViewModeLive ViewMode = iota
	ViewModeHistory
	ViewModeUsage
)

func runLiveView(interval time.Duration, webEnabled bool, webPort int) {
	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start web server in background if requested
	var webURL string
	var webBrowseURL string
	if webEnabled {
		if web.ProbeCSMServer(webPort) {
			webBrowseURL = fmt.Sprintf("http://localhost:%d", webPort)
			webURL = webBrowseURL + " (existing server)"
		} else {
			srv := web.NewServer(webPort)
			webErrCh, err := srv.Start(ctx)
			if err != nil {
				cancel()
				fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
				os.Exit(1)
			}
			go func() {
				if err := <-webErrCh; err != nil {
					fmt.Fprintf(os.Stderr, "\nWeb server error: %v\n", err)
				}
			}()
			webBrowseURL = "http://" + srv.Addr()
			webURL = webBrowseURL
		}
	}

	// Set up keyboard input
	if err := ui.SetupRawInput(); err != nil {
		cancel()
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

	// Throttle history view refreshes (data changes infrequently)
	var lastHistoryRender time.Time

	// Render function that respects current mode
	render := func() {
		switch viewMode {
		case ViewModeHistory:
			ui.ClearScreen()
			sessions, _ := session.DiscoverHistory(historyDays)
			ui.RenderHistory(sessions, historyDays, true)
		case ViewModeUsage:
			ui.ClearScreen()
			usage := session.ComputeUsage()
			apiQuota := session.FetchAPIQuota()
			ui.RenderUsage(usage, apiQuota, true)
		default:
			sessions, _ := session.Discover()
			ui.RenderLive(sessions, webURL)
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
					lastHistoryRender = time.Now()
				}
			case 'l', 'L':
				if viewMode != ViewModeLive {
					viewMode = ViewModeLive
					render()
				}
			case 'u', 'U':
				if viewMode != ViewModeUsage {
					viewMode = ViewModeUsage
					render()
				}
			case 'r', 'R':
				if viewMode == ViewModeUsage {
					render()
				}
			case 'w', 'W':
				if webBrowseURL != "" {
					openBrowser(webBrowseURL)
				}
			case 3: // Ctrl+C
				cancel()
				return
			}
		case <-ticker.C:
			if viewMode == ViewModeUsage {
				continue
			}
			if viewMode == ViewModeHistory && time.Since(lastHistoryRender) < 30*time.Second {
				continue
			}
			render()
			if viewMode == ViewModeHistory {
				lastHistoryRender = time.Now()
			}
		}
	}
}

// runWebOnly starts the web dashboard server without the terminal UI.
// This is used by the macOS menu bar app and other headless integrations.
func runWebOnly(webPort int) {
	if web.ProbeCSMServer(webPort) {
		fmt.Printf("csm web dashboard is already running at http://localhost:%d\n", webPort)
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	srv := web.NewServer(webPort)
	webErrCh, err := srv.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Web dashboard running at http://%s\n", srv.Addr())

	select {
	case <-sigCh:
		cancel()
	case err := <-webErrCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
			os.Exit(1)
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

// openBrowser opens the given URL in the default browser
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	cmd.Start()
}
