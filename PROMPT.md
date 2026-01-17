# Claude Sessions Monitor - Project Kickoff

## Overview

Build a CLI tool in Go called `csm` (Claude Sessions Monitor) that monitors Claude Code sessions and displays their status in the terminal. The tool should have zero external dependencies (standard library only) for easy distribution as a single binary.

## Problem Being Solved

When running multiple Claude Code sessions in different terminal tabs/windows, it's hard to keep track of which sessions are:
- Actively working
- Waiting for user input/approval
- Idle or finished

This tool watches `~/.claude/projects/` and provides a live dashboard showing all sessions and their current status.

## Core Features

### 1. Session Discovery
- Scan `~/.claude/projects/` directory
- Each subdirectory is a project (URL-encoded path like `%2Fhome%2Fuser%2Fproject`)
- Find the most recent `.jsonl` log file in each project
- Parse JSONL to determine session state

### 2. Status Detection
Parse the JSONL log entries to determine status:

| Status | Condition |
|--------|-----------|
| **Working** | Recent assistant message, still streaming/processing |
| **Needs Approval** | Last entry is assistant with `tool_use`, waiting for user to approve |
| **Waiting** | Turn ended, waiting for user's next prompt |
| **Idle** | No activity for 5+ minutes |

Log entry types to look for:
- `type: "assistant"` with `content[].type: "tool_use"` → tool requested
- `type: "user"` with `content[].type: "tool_result"` → tool approved/executed
- `type: "system"` with `turn_duration` → turn completed
- Timestamps for activity tracking

### 3. CLI Modes

```
csm                    # Live view (default) - auto-updating dashboard
csm -l                 # List once and exit
csm -l -json           # List as JSON
csm -v                 # Version
csm -interval 5s       # Custom refresh interval (default 2s)
```

### 4. Live View Display

```
🤖 Claude Code Sessions

● Working: 2  ⚠ Needs Input: 1  ◉ Waiting: 0  ○ Idle: 3

  STATUS          PROJECT                             LAST ACTIVITY   CURRENT TASK
  ────────────────────────────────────────────────────────────────────────────────
  ● Working       myorg/api-server                    5s ago          Implementing auth middleware...
  ● Working       myorg/frontend                      12s ago         Using: Edit
  ⚠ Needs Input   personal/side-project               45s ago         Using: Bash
  ○ Idle          work/legacy-app                     15m ago         -

  Press Ctrl+C to quit
```

Use ANSI colors:
- Green for Working
- Yellow/Orange for Needs Input  
- Blue for Waiting
- Gray for Idle

### 5. List Mode Output

```
STATUS          PROJECT                             LAST ACTIVITY   TASK
────────────────────────────────────────────────────────────────────────────────
● Working       myorg/api-server                    5s ago          Implementing auth...
⚠ Needs Input   personal/side-project               45s ago         Using: Bash
```

### 6. JSON Output

```json
[
  {"project": "myorg/api-server", "status": "Working", "last_activity": "2024-01-15T10:30:00Z", "task": "Implementing..."},
  {"project": "personal/side-project", "status": "Needs Approval", "last_activity": "2024-01-15T10:29:15Z", "task": "Using: Bash"}
]
```

## Project Structure

```
claude-sessions-monitor/
├── main.go                      # CLI entry point, flag parsing
├── internal/
│   ├── session/
│   │   └── session.go           # Session discovery and JSONL parsing
│   ├── watcher/
│   │   └── watcher.go           # File system watching for changes
│   └── ui/
│       └── ui.go                # Terminal rendering (ANSI)
├── go.mod
├── README.md
├── CHANGELOG.md
└── Makefile                     # Build targets for multiple platforms
```

## Technical Requirements

1. **Zero external dependencies** - use only Go standard library
2. **Cross-platform** - must work on macOS and Linux
3. **Single binary** - easy to distribute
4. **Efficient** - poll filesystem changes, don't hammer CPU
5. **Graceful** - handle Ctrl+C cleanly, restore cursor

## Build & Distribution

Makefile should support:
```makefile
build:           # Build for current platform
build-all:       # Build for linux/darwin, amd64/arm64
install:         # Install to ~/.local/bin
```

## JSONL Log Format Reference

Each line in the log file is a JSON object:

```json
{"type": "user", "message": {"role": "user", "content": [{"type": "text", "text": "..."}]}, "timestamp": "..."}
{"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "..."}]}, "timestamp": "..."}
{"type": "assistant", "message": {"role": "assistant", "content": [{"type": "tool_use", "name": "Edit", "id": "..."}]}, "timestamp": "..."}
{"type": "user", "message": {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "..."}]}, "timestamp": "..."}
{"type": "system", "message": {"turn_duration": 5.2}, "timestamp": "..."}
```

## Getting Started

1. Initialize the Go module
2. Create the directory structure  
3. Implement session discovery first (can test with -l flag)
4. Add the watcher for live updates
5. Implement the terminal UI
6. Add Makefile for builds
7. Write README with installation instructions

Start by exploring `~/.claude/projects/` to understand the actual structure, then implement session parsing.
