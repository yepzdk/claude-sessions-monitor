# Claude Sessions Monitor (csm)

A lightweight CLI tool to monitor your Claude Code sessions across multiple projects.

## Features

- **Live dashboard** showing all active Claude Code sessions
- **Process detection** distinguishes running vs inactive sessions
- **Session summaries** from JSONL logs
- **Status indicators**: Working, Needs Input, Waiting, Idle, Inactive
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

# Custom refresh interval
csm -interval 5s

# Show version
csm -v
```

## Status Types

| Symbol | Status | Description |
|--------|--------|-------------|
| ● | Working | Session is actively processing |
| ⚠ | Needs Input | Waiting for user to approve a tool use |
| ◉ | Waiting | Turn completed, waiting for next prompt |
| ○ | Idle | Claude running but no activity for 5+ minutes |
| ◌ | Inactive | No Claude process running for this project |

## Screenshot

```
🤖 Claude Code Sessions

● Working: 1  ⚠ Needs Input: 1  ◉ Waiting: 0  ○ Idle: 2  ◌ Inactive: 5

  STATUS          PROJECT                             LAST ACTIVITY   SUMMARY
  ───────────────────────────────────────────────────────────────────────────────────────────
  ● Working       myorg/api-server                    5s ago          Implementing auth middleware
  ⚠ Needs Input   work/claude-sessions-monitor        12s ago         Using: Bash
  ○ Idle          personal/side-project               8m ago          Fix login validation
  ○ Idle          work/frontend                       12m ago         Component refactoring

  Press Ctrl+C to quit
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
