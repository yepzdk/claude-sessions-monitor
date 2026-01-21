# Claude Sessions Monitor (csm)

A lightweight CLI tool to monitor your Claude Code sessions across multiple projects.

## Features

- **Live dashboard** showing all active Claude Code sessions
- **History view** to browse past sessions with activity summaries
- **Process detection** distinguishes running vs inactive sessions
- **Ghost detection** identifies orphaned Claude processes
- **Last message display** shows recent Claude responses
- **Git branch display** shows current branch for each session
- **Status indicators**: Working, Needs Input, Waiting
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
| `Ctrl+C` | Quit |

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
