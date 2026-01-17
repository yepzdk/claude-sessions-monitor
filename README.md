# Claude Sessions Monitor (csm)

A lightweight CLI tool to monitor your Claude Code sessions across multiple projects.

## Features

- **Live dashboard** showing all active Claude Code sessions
- **Status indicators**: Working, Needs Input, Waiting, Idle
- **Zero dependencies** - single binary, easy to install
- **Cross-platform** - macOS and Linux

## Installation

### From releases

Download the latest binary from [Releases](https://github.com/itk-dev/claude-sessions-monitor/releases) and add to your PATH.

### Build from source

```bash
git clone https://github.com/itk-dev/claude-sessions-monitor.git
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
| ○ | Idle | No activity for 5+ minutes |

## Screenshot

```
🤖 Claude Code Sessions

● Working: 2  ⚠ Needs Input: 1  ◉ Waiting: 0  ○ Idle: 1

  STATUS          PROJECT                             LAST ACTIVITY   CURRENT TASK
  ────────────────────────────────────────────────────────────────────────────────
  ● Working       myorg/api-server                    5s ago          Implementing auth middleware...
  ⚠ Needs Input   personal/side-project               45s ago         Using: Bash
  ○ Idle          work/legacy-app                     15m ago         -

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
