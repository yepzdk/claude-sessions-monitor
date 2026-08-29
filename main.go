package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/yepzdk/claude-sessions-monitor/internal/jump"
	"github.com/yepzdk/claude-sessions-monitor/internal/session"
	"github.com/yepzdk/claude-sessions-monitor/internal/ui"
	"github.com/yepzdk/claude-sessions-monitor/internal/upgrade"
	"github.com/yepzdk/claude-sessions-monitor/internal/web"
)

// version is injected by the Makefile via -ldflags. A `go install` build has
// no ldflags, so fall back to the module version Go stamps into the binary;
// "(devel)" is what a local checkout reports and is no better than "dev".
var version = "dev"

func main() {
	if version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
	}
	// internal/session can't reach main's -ldflags-injected version on its own,
	// and it needs it to identify csm in outgoing API requests.
	session.SetVersion(version)

	// Parse flags
	listOnce := flag.Bool("l", false, "List sessions once and exit")
	jsonOutput := flag.Bool("json", false, "Output as JSON (requires -l)")
	showVersion := flag.Bool("v", false, "Show version")
	interval := flag.Duration("interval", 2*time.Second, "Refresh interval for live view")
	historyMode := flag.Bool("history", false, "Show session history")
	historyDays := flag.Int("days", 7, "Number of days for history")
	killGhosts := flag.Bool("kill-ghosts", false, "Find and terminate ghost (orphaned) Claude processes")
	webMode := flag.Bool("web", false, "Start web dashboard server")
	webOnly := flag.Bool("web-only", false, "Start web dashboard server without terminal UI (headless)")
	webPort := flag.Int("port", 9847, "Port for web dashboard")
	doUpgrade := flag.Bool("upgrade", false, "Upgrade csm to the latest release")
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

	// Handle upgrade mode. Before every other mode: it neither reads sessions
	// nor draws anything, so nothing below needs to have run first.
	if *doUpgrade {
		os.Exit(upgrade.Run(version, os.Stdout))
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
		ui.RenderHistory(sessions, *historyDays, false, "")
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
		os.Exit(runWebOnly(*webPort))
	}

	// Live view mode
	os.Exit(runLiveView(*interval, *webMode, *webPort))
}

// nextHarnessFilter cycles the live view's harness filter: everything, then each
// agent in turn.
//
// This is a view filter and deliberately not a CLI flag. Discovery always covers
// every agent on the machine -- that is the point of the tool -- and which rows
// you want to read right now is a property of the moment, not of how csm was
// started. A flag would also have had to mean something for the web dashboard,
// which has no key to press to undo it and serves more than one client.
func nextHarnessFilter(current session.Harness) session.Harness {
	switch current {
	case "":
		return session.HarnessClaude
	case session.HarnessClaude:
		return session.HarnessOMP
	default:
		return ""
	}
}

// ViewMode represents the current display mode
type ViewMode int

const (
	ViewModeLive ViewMode = iota
	ViewModeHistory
	ViewModeUsage
)

// runLiveView returns the process exit code. It calls os.Exit nowhere itself:
// the terminal is on the alternate screen with echo off from the moment the
// defer below is registered, and an exit that skipped it would leave the user
// at an invisible prompt.
func runLiveView(interval time.Duration, webEnabled bool, webPort int) (code int) {
	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// A failure inside the web server reaches the main loop rather than being
	// printed here: this goroutine writes onto the alternate screen, where the
	// next frame overwrites it, and the deferred restore below never runs.
	webFatal := make(chan error, 1)

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
				fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
				return 1
			}
			go func() {
				if err := <-webErrCh; err != nil {
					webFatal <- err
				}
			}()
			webBrowseURL = "http://" + srv.Addr()
			webURL = webBrowseURL
		}
	}

	// Set up keyboard input
	if err := ui.SetupRawInput(); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up keyboard input: %v\n", err)
		return 1
	}

	// Start keyboard reader
	keyCh := make(chan rune, 1)
	done := make(chan struct{})
	go ui.ReadKey(keyCh, done)

	// Track current view mode
	viewMode := ViewModeLive
	historyDays := 7

	// Row selection for jumping. -1 means nothing is selected; the live view
	// starts that way so the first arrow press lands on the top row.
	selected := -1
	actionMsg := ""
	// Sessions as of the last render, so a keypress acts on exactly the rows
	// the user can see rather than re-discovering and racing the ticker.
	var visible []session.Session
	// Harness filter, cycled with `f`. Starts at "" — every agent — because
	// tracking all of them is the default and the filter is only a reading aid.
	var filter session.Harness
	// Whether the last render saw more than one agent among the *visible* rows.
	// The `f` handler reads it so the key is inert on a single-agent machine,
	// which is what the footer promises by not advertising it there.
	mixed := false

	// Check for a newer release off the render path: upgrade.Notice blocks on
	// the network, and a frame must never wait on github.com. The result
	// arrives once, over a channel, so the render loop owns the string and
	// there is nothing to race on.
	updateNotice := ""
	noticeCh := make(chan string, 1)
	go func() {
		if n := upgrade.Notice(version); n != "" {
			noticeCh <- n
		}
	}()

	// Claude status: fetch on-demand (user interaction), use cached on ticker
	var lastClaudeStatus *session.ClaudeStatus
	refreshClaudeStatus := func() {
		lastClaudeStatus = session.FetchClaudeStatus()
	}

	// Take over the screen and ensure cleanup on exit
	var fatalErr error
	ui.EnterAltScreen()
	ui.HideCursor()
	defer func() {
		close(done)
		ui.CleanupRawInput()
		ui.ShowCursor()
		ui.ResetTerminalTitle()
		ui.ExitAltScreen()
		if fatalErr != nil {
			// After the restore above, so the message lands on the shell's
			// screen instead of the one that is about to be discarded.
			fmt.Fprintf(os.Stderr, "csm: %v\n", fatalErr)
			code = 1
			return
		}
		fmt.Println("Goodbye!")
	}()

	// Throttle history view refreshes (data changes infrequently)
	var lastHistoryRender time.Time

	// Render function that respects current mode
	render := func() {
		switch viewMode {
		case ViewModeHistory:
			ui.MoveCursorHome()
			sessions, err := session.DiscoverHistory(historyDays)
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			ui.RenderHistory(sessions, historyDays, true, errMsg)
		case ViewModeUsage:
			ui.MoveCursorHome()
			usage := session.ComputeUsage()
			apiQuota := session.FetchAPIQuota()
			ui.RenderUsage(usage, apiQuota, true)
		default:
			sessions, err := session.Discover()
			// An empty dashboard and a failed scan look identical once the
			// error is dropped, so the reason goes into the frame rather than
			// leaving csm to report "No active sessions." either way.
			msg := actionMsg
			if err != nil {
				msg = "Cannot read sessions: " + err.Error()
			}
			// Decided over the rows the frame will actually draw, and before the
			// filter: RenderLive only shows ActiveSessions, so counting the
			// Inactive ones tagged every row `[cc]` and advertised `f` on a
			// Claude-only machine that merely had one stale omp bucket on disk.
			// Taking it before the filter keeps the tag stable while `f` cycles,
			// so the rows do not re-flow under the user.
			mixed = ui.MixedHarnesses(ui.ActiveSessions(sessions))
			sessions = ui.FilterByHarness(sessions, filter)
			// Sessions come and go between frames, so the selection is clamped
			// on every render rather than only when a key moves it.
			visible = ui.ActiveSessions(sessions)
			if selected >= len(visible) {
				selected = len(visible) - 1
			}
			ui.RenderLive(ui.LiveView{
				Sessions:     sessions,
				WebURL:       webURL,
				ClaudeStatus: lastClaudeStatus,
				Selected:     selected,
				ActionMsg:    msg,
				UpdateNotice: updateNotice,
				Filter:       filter,
				Mixed:        mixed,
			})
		}
		// Erase anything left over below this frame from a previous, longer
		// one, once per render cycle rather than each view remembering to.
		ui.EraseToEnd()
	}

	// Initial render
	refreshClaudeStatus()
	render()

	// Main loop with both watcher and keyboard input
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			cancel()
			return 0
		case <-ctx.Done():
			return 0
		case fatalErr = <-webFatal:
			cancel()
			return 0
		case updateNotice = <-noticeCh:
			render()
		case key := <-keyCh:
			// Any keypress clears the previous action's feedback, so a stale
			// message never sits under a table it no longer describes.
			actionMsg = ""
			switch key {
			case ui.KeyUp, ui.KeyDown:
				if viewMode != ViewModeLive || len(visible) == 0 {
					break
				}
				switch {
				case selected < 0:
					// First arrow press selects rather than moving, so Up is
					// not a no-op on a freshly started view.
					selected = 0
				case key == ui.KeyUp && selected > 0:
					selected--
				case key == ui.KeyDown && selected < len(visible)-1:
					selected++
				}
				render()
			case ui.KeyEnter, '\n':
				if viewMode != ViewModeLive || selected < 0 || selected >= len(visible) {
					break
				}
				res, err := jump.Focus(visible[selected])
				if err != nil {
					actionMsg = err.Error()
				} else {
					actionMsg = res.Message()
				}
				render()
			case 'h', 'H':
				if viewMode != ViewModeHistory {
					viewMode = ViewModeHistory
					selected = -1
					render()
					lastHistoryRender = time.Now()
				}
			case 'l', 'L':
				if viewMode != ViewModeLive {
					viewMode = ViewModeLive
					refreshClaudeStatus()
					render()
				}
			case 'u', 'U':
				if viewMode != ViewModeUsage {
					viewMode = ViewModeUsage
					selected = -1
					render()
				}
			case 'r', 'R':
				if viewMode == ViewModeUsage {
					render()
				}
			case 'f', 'F':
				// Only where the footer offers it. On a single-agent machine the
				// key used to cycle anyway, landing the user on "No active
				// sessions." with no footer entry naming the key that gets them
				// back out.
				if viewMode == ViewModeLive && mixed {
					filter = nextHarnessFilter(filter)
					// The row count changes with the filter, so a selection
					// carried over would point at a different session.
					selected = -1
					render()
				}
			case 'w', 'W':
				if webBrowseURL != "" {
					// Confirming the launch matters as much as reporting the
					// failure: openBrowser returns once the child is running,
					// so a browser that starts and then gives up looks exactly
					// like a key that was never registered.
					if err := openBrowser(webBrowseURL); err != nil {
						actionMsg = "Cannot open a browser: " + err.Error()
					} else {
						actionMsg = "Opening " + webBrowseURL
					}
					render()
				}
			case 3: // Ctrl+C
				cancel()
				return 0
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

// runWebOnly starts the web dashboard server without the terminal UI and
// returns the process exit code. This is used by the macOS menu bar app and
// other headless integrations.
func runWebOnly(webPort int) (code int) {
	if web.ProbeCSMServer(webPort) {
		fmt.Printf("csm web dashboard is already running at http://localhost:%d\n", webPort)
		return 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	srv := web.NewServer(webPort)
	webErrCh, err := srv.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
		return 1
	}

	fmt.Printf("Web dashboard running at http://%s\n", srv.Addr())

	select {
	case <-sigCh:
		cancel()
	case err := <-webErrCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Web server error: %v\n", err)
			return 1
		}
	}
	return 0
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

	killed, failed, err := session.KillGhostProcesses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error killing ghost processes: %v\n", err)
		os.Exit(1)
	}

	if len(killed) == 0 {
		fmt.Println("No processes were terminated (they may have already exited).")
	} else {
		fmt.Printf("Terminated %d ghost process(es).\n", len(killed))
	}

	for _, f := range failed {
		fmt.Fprintf(os.Stderr, "  could not signal PID %d (%s): %v\n",
			f.Ghost.PID, f.Ghost.Project, f.Err)
	}
	if len(failed) > 0 {
		os.Exit(1)
	}
}
