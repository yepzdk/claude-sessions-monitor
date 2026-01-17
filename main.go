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
	"github.com/itk-dev/claude-sessions-monitor/internal/watcher"
)

var version = "dev"

func main() {
	// Parse flags
	listOnce := flag.Bool("l", false, "List sessions once and exit")
	jsonOutput := flag.Bool("json", false, "Output as JSON (requires -l)")
	showVersion := flag.Bool("v", false, "Show version")
	interval := flag.Duration("interval", 2*time.Second, "Refresh interval for live view")
	flag.Parse()

	// Handle version
	if *showVersion {
		fmt.Printf("csm version %s\n", version)
		os.Exit(0)
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

func runLiveView(interval time.Duration) {
	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		cancel()
	}()

	// Hide cursor and ensure we show it again on exit
	ui.HideCursor()
	defer func() {
		ui.ShowCursor()
		ui.ResetTerminalTitle()
		ui.ClearScreen()
		fmt.Println("Goodbye!")
	}()

	// Start watching
	w := watcher.New(interval)
	w.Watch(ctx, func(sessions []session.Session) {
		ui.RenderLive(sessions)
	})
}
