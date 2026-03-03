# Claude Sessions Monitor (csm)

A lightweight CLI tool to monitor your Claude Code sessions across multiple projects.

## Features

- **Live dashboard** showing all active Claude Code sessions
- **Web dashboard** with `--web` flag for rich session inspection in the browser
- **History view** to browse past sessions with activity summaries
- **Process detection** distinguishes running vs inactive sessions
- **Ghost detection** identifies orphaned Claude processes
- **Last message display** shows recent Claude responses
- **Git branch display** shows current branch for each session
- **Status indicators**: Working, Needs Input, Waiting
- **Token quota tracking** with color-coded progress bar (opt-in via `-quota-limit`)
- **Session badges**: Desktop [D], Unsandboxed [!S], Ghost [ghost]
- **Zero dependencies** - single binary, easy to install
- **Cross-platform** - macOS and Linux

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap yepzdk/tools
brew install csm
```

### From releases

Download the latest binary from [Releases](https://github.com/yepzdk/claude-sessions-monitor/releases) and add to your PATH.

### Build from source

```bash
git clone https://github.com/yepzdk/claude-sessions-monitor.git
cd claude-sessions-monitor
make install
```

## Usage

```bash
# Live view (default)
csm

# Live view with web dashboard
csm --web

# Web dashboard on custom port
csm --web --port 3000

# List sessions once
csm -l

# Output as JSON
csm -l -json

# Show session history (last 7 days)
csm -history

# Show session history for last 30 days
csm -history -days 30

# Find and kill ghost (orphaned) processes
csm -kill-ghosts

# Token quota tracking (5h rolling window, 5M token limit)
csm -quota-limit 5000000

# Count only output tokens toward quota
csm -quota-limit 5000000 -quota-tokens output

# Custom rolling window (e.g. 1 hour)
csm -quota-limit 5000000 -quota-window 1h

# Custom refresh interval
csm -interval 5s

# Show version
csm -v
```

### Keyboard shortcuts (live view)

| Key | Action |
|-----|--------|
| `h` | Switch to history view |
| `l` | Switch to live view |
| `w` | Open web dashboard in browser (when `--web` is active) |
| `Ctrl+C` | Quit |

### Token quota tracking

Track token usage against your plan's limits with `-quota-limit`. The quota bar appears in both the terminal live view and the web dashboard.

| Flag | Default | Description |
|------|---------|-------------|
| `-quota-limit N` | `0` (disabled) | Token limit for your plan |
| `-quota-window D` | `5h` | Rolling window duration |
| `-quota-tokens T` | `all` | Which tokens to count: `all` or `output` |

The progress bar is color-coded: green (<75%), yellow (75-90%), red (>90%). A renewal countdown shows when the oldest tokens in the window will expire.

### Web dashboard

Start with `csm --web` to run the web dashboard alongside the terminal UI. The dashboard is available at `http://localhost:9847` by default.

Features:
- **Live sessions** with status indicators, context bars, and auto-refresh via SSE
- **Token quota widget** with progress bar (when `-quota-limit` is set)
- **History view** with search/filter and date grouping
- **Session detail panels** with metrics (token usage, tool breakdown, turn count) and full message timeline
- **Timeline filters** to show All, Assistant, or User messages
- REST API: `/api/sessions`, `/api/history`, `/api/quota`, `/api/sessions/timeline`, `/api/sessions/metrics`
- Embedded in the binary via `go:embed` — no external files or build step needed

## Status Types

| Symbol | Status | Description |
|--------|--------|-------------|
| ● | Working | Session is actively processing |
| ▲ | Needs Input | Waiting for user to approve a tool use |
| ◉ | Waiting | Turn completed, waiting for next prompt |
| ◌ | Inactive | No Claude process running (shown in history) |

## Screenshot

```
Claude Code Sessions

● Working: 1  ▲ Needs Input: 1  ◉ Waiting: 0

STATUS          PROJECT                             LAST ACTIVITY   LAST MESSAGE
───────────────────────────────────────────────────────────────────────────────────────────
● Working       myorg/api-server @main              5s ago          Implementing auth middleware
▲ Needs Input   work/claude-sessions-monitor @feat  12s ago         Let me check the git status

h: history | Ctrl+C: quit
```

## Building

```bash
# Build for current platform
make build

# Build for all platforms (darwin/linux, amd64/arm64)
make build-all

# Clean build artifacts
make clean
```

## How it works

The tool monitors `~/.claude/projects/` where Claude Code stores session logs. It parses the JSONL log files to determine each session's current state based on the most recent entries.

## License

MIT
