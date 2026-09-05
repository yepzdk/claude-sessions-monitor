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

	opts := registerFlags(flag.CommandLine)
	flag.Parse()

	// flag stops parsing at the first non-flag argument and leaves the rest in
	// flag.Args(): `csm upgrade` dropped the word entirely and started the
	// dashboard, and `csm upgrade -v` blamed the user for a flag csm has.
	// resolveArgs consumes the subcommand and hands what follows back to the
	// FlagSet, so a flag means the same thing on either side of the word.
	wantUpgrade, err := resolveArgs(flag.Args(), flag.CommandLine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		flag.Usage()
		os.Exit(2)
	}
	if wantUpgrade {
		opts.doUpgrade = true
	}

	// Check for conflicting flags
	if opts.webMode && opts.webOnly {
		fmt.Fprintf(os.Stderr, "Error: --web and --web-only are mutually exclusive\n")
		os.Exit(1)
	}
	if opts.doUpgrade {
		if err := upgradeConflicts(flag.CommandLine); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Handle version
	if opts.showVersion {
		fmt.Printf("csm version %s\n", version)
		os.Exit(0)
	}

	// Handle upgrade mode. Before every other mode: it neither reads sessions
	// nor draws anything, so nothing below needs to have run first.
	if opts.doUpgrade {
		os.Exit(upgrade.Run(version, os.Stdout, opts.assumeYes))
	}

	// Handle kill-ghosts mode
	if opts.killGhosts {
		handleKillGhosts()
		return
	}

	// Handle history mode
	if opts.historyMode {
		sessions, err := session.DiscoverHistory(opts.historyDays)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering history: %v\n", err)
			os.Exit(1)
		}
		ui.RenderHistory(sessions, opts.historyDays, false, "")
		return
	}
	// Handle list mode
	if opts.listOnce {
		sessions, err := session.Discover()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering sessions: %v\n", err)
			os.Exit(1)
		}

		if opts.jsonOutput {
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
	if opts.webOnly {
		os.Exit(runWebOnly(opts.webPort))
	}

	// Live view mode
	os.Exit(runLiveView(opts.interval, opts.webMode, opts.webPort))
}

// options is every flag csm takes. It exists so main and the tests register
// the same set: resolveArgs hands leftover words back to the FlagSet, and a
// test that declared its own flags would be checking a parser csm does not use.
type options struct {
	listOnce    bool
	jsonOutput  bool
	showVersion bool
	interval    time.Duration
	historyMode bool
	historyDays int
	killGhosts  bool
	webMode     bool
	webOnly     bool
	webPort     int
	doUpgrade   bool
	assumeYes   bool
}

func registerFlags(fs *flag.FlagSet) *options {
	var o options
	fs.BoolVar(&o.listOnce, "l", false, "List sessions once and exit")
	fs.BoolVar(&o.jsonOutput, "json", false, "Output as JSON (requires -l)")
	fs.BoolVar(&o.showVersion, "v", false, "Show version")
	fs.DurationVar(&o.interval, "interval", 2*time.Second, "Refresh interval for live view")
	fs.BoolVar(&o.historyMode, "history", false, "Show session history")
	fs.IntVar(&o.historyDays, "days", 7, "Number of days for history")
	fs.BoolVar(&o.killGhosts, "kill-ghosts", false, "Find and terminate ghost (orphaned) Claude processes")
	fs.BoolVar(&o.webMode, "web", false, "Start web dashboard server")
	fs.BoolVar(&o.webOnly, "web-only", false, "Start web dashboard server without terminal UI (headless)")
	fs.IntVar(&o.webPort, "port", 9847, "Port for web dashboard")
	fs.BoolVar(&o.doUpgrade, "upgrade", false, "Upgrade csm to the latest release")
	// One variable, two spellings: -y is what a hand types, --yes is what a
	// script reads back. Go treats --yes and -yes as the same flag.
	fs.BoolVar(&o.assumeYes, "y", false, "Upgrade without asking for confirmation")
	fs.BoolVar(&o.assumeYes, "yes", false, "Upgrade without asking for confirmation")
	return &o
}

// resolveArgs interprets the arguments left over after flag parsing, reporting
// whether they asked for an upgrade.
//
// csm's interface is flags, but `csm upgrade` is what a user reaches for before
// reading -h, and `update` is the word half the tools they already use spell it
// with. Both are accepted rather than corrected: the alternative is a person
// who asked to upgrade watching a dashboard start instead, with nothing on
// screen to say why. Anything else is an error, because silently ignoring an
// argument means doing something other than what was typed.
//
// The loop is what makes flags work after the word. flag stops parsing at the
// first non-flag argument, so `csm upgrade -v` left "-v" in flag.Args() and it
// was reported as an argument csm cannot run -- for a flag csm has. Consuming
// the subcommand and re-parsing the remainder puts it back in front of the
// FlagSet. A word after the subcommand is still an error, and still names
// itself: "upgrade takes no arguments" is actionable where the generic
// "unknown argument" would blame the wrong word.
func resolveArgs(args []string, fs *flag.FlagSet) (upgrade bool, err error) {
	verb := ""
	for len(args) > 0 {
		switch {
		case upgrade:
			return false, fmt.Errorf("%s takes no arguments, got %q", verb, args[0])
		case args[0] == "upgrade", args[0] == "update":
			upgrade, verb = true, args[0]
		default:
			return false, fmt.Errorf("unknown argument %q; csm takes flags, plus `upgrade`", args[0])
		}
		if err := fs.Parse(args[1:]); err != nil {
			return false, err
		}
		args = fs.Args()
	}
	return upgrade, nil
}

// upgradeConflicts reports a flag that cannot mean anything alongside an
// upgrade. `csm -l upgrade` used to run the upgrade and drop the -l silently,
// which is the same "ran a different command than the one typed" that the
// argument errors above exist to prevent. Refusing it matches how --web and
// --web-only are handled.
//
// Only flags actually given are considered, which is why this reads the FlagSet
// rather than options: a false in the struct cannot be told from an unset flag.
func upgradeConflicts(fs *flag.FlagSet) error {
	clash := ""
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "upgrade", "v", "y", "yes":
			// -upgrade is the request itself, -v prints the version and exits
			// before the upgrade runs, and -y answers its confirmation.
		default:
			if clash == "" {
				clash = f.Name
			}
		}
	})
	if clash == "" {
		return nil
	}
	return fmt.Errorf("-%s and upgrade are mutually exclusive", clash)
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
	roster := session.Harnesses()
	for i, h := range roster {
		if h == current {
			if i+1 < len(roster) {
				return roster[i+1]
			}
			return ""
		}
	}
	// "" and anything no longer in the roster both restart the cycle.
	return roster[0]
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
			// Asked of every session on the machine, not of the rows this frame
			// will draw: the live view shows only active ones, so a rule read
			// off them turns the badge -- and the six columns the origin cell
			// grows by to hold it -- on and off as sessions go idle, re-flowing
			// the table under the user. session.MixedHarnesses bounds it by
			// recency instead, and the `-l` listing and web dashboard ask it the
			// same way, so the three surfaces cannot disagree.
			mixed = session.MixedHarnesses(sessions)
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
				//
				// A filter already set is reason enough on its own: it outlives
				// the mixed dashboard that allowed it -- start a session of the
				// other agent, filter to it, let it go idle -- and gating the
				// key on `mixed` alone left the rows hidden with nothing able to
				// bring them back short of restarting csm.
				if viewMode == ViewModeLive && (mixed || filter != "") {
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
