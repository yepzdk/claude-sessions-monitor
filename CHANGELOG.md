# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- Sessions actively using tools, hooks, or subagents no longer flicker to "Waiting" — progress heartbeats from Claude Code logs are now tracked
- Multi-tool_use detection: all tool calls in an assistant message are now checked, not just the first
- Extended assistant "Working" window from 30 seconds to 2 minutes to reduce false "Waiting" during brief log gaps
- Use log file modification time to detect active streaming writes, preventing "Waiting" during early response generation
- Context percentage now uses model-specific context window sizes (Opus 4.6 and Sonnet 4.6 use 1M, others use 200K)

### Added

- Parse `stop_reason` field from Claude Code JSONL logs for more accurate status detection
- Track `progress`, `hook_progress`, and `agent_progress` log entries as activity heartbeats
- Detect multiple concurrent Claude sessions in the same project directory (each shown as a separate row/card)
- Show Claude service status from status.claude.com in terminal live view and web dashboard
- `make menubar-install` target for one-step .app installation with quarantine removal
- README troubleshooting section for macOS Gatekeeper warning
- CSMMenuBar `.app` bundles attached to GitHub Releases (arm64 + amd64)
- Homebrew cask: `brew install --cask yepzdk/tools/csm-menubar`
- macOS menu bar app can be packaged as a `.app` bundle (`make menubar-app`) for Spotlight/Launchpad/Applications
- macOS menu bar app (SwiftUI, macOS 13+) for persistent session visibility without a terminal or browser
  - Dynamic status icon with color reflecting aggregate session state
  - Session popover with project, status, branch, context bar, and last activity
  - Smart `csm --web-only` process management (detects existing server, starts if needed, cleans up on quit)
  - "Open Web Dashboard" link for history and detailed views
- `--web-only` flag for headless web server mode (no terminal UI required)

### Changed

- Terminal: Claude service status is fetched on-demand (startup + key press) instead of every ticker cycle
- Web: Claude service status polling pauses when the browser tab is hidden and resumes on visibility
- Menu bar app defers service startup to popover `.onAppear` instead of `init()`
- Menu bar app reads port from `CSM_PORT` environment variable (default: 9847)
- Menu bar app terminates `csm` child process off the main thread to prevent UI freeze
- macOS menu bar app now bundles the `csm` binary — no separate installation required
- Make usage/quota fetching fully on-demand instead of periodic polling
- Terminal: usage data fetched only on view entry (`u`) or manual refresh (`r`)
- Web: usage data fetched via REST on tab switch or refresh button click, no longer broadcast via SSE
- Increase API quota cache TTL from 30s to 60s to reduce Anthropic API request frequency
- Extract reusable CI workflow for menu bar release (`.github/workflows/release-menubar.yaml`)
- Menu bar app `fetchSessions()` uses async/await instead of callback-based `dataTask`

### Fixed

- Unhelpful error when starting csm while another instance is already running on the same port
- Menu bar app amd64 build failing due to unsigned cross-compiled `csm` binary inside `.app` bundle
- Menu bar app shows error message when `csm` binary is not found instead of generic "No sessions found"
- Removed empty `AppDelegate` class and duplicate `.gitignore` entry
- README now correctly references `--web-only` flag for menu bar app
- Include output tokens in context window calculation to match Claude Code's reported usage
- Menu bar app "Web Dashboard" link now uses configured port instead of hardcoded 9847
- Menu bar app process cleanup race on quit — `csm` child process is now terminated synchronously
- `--web` and `--web-only` flags now report an error when used together instead of silently ignoring `--web`
- Menu bar app displays "just now" instead of "0s ago" for very recent activity
- Menu bar app server startup uses retry loop (3 × 500ms) instead of a single 1s sleep
