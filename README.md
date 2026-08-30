# ~~Claude~~ Coding Sessions Monitor (csm)

A lightweight CLI tool to monitor your coding agent sessions — Claude Code and
Oh My Pi — across multiple projects.

> csm started life as *Claude* Sessions Monitor. It watches
> [Oh My Pi](https://github.com/badlogic/oh-my-pi) sessions too now, so the C
> stands for Coding. Same `csm`, same install, nothing to migrate — the
> repository keeps its old name so existing clones and `go install` paths still
> work.

[![CI](https://github.com/yepzdk/claude-sessions-monitor/actions/workflows/ci.yaml/badge.svg)](https://github.com/yepzdk/claude-sessions-monitor/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/yepzdk/claude-sessions-monitor?logo=github)](https://github.com/yepzdk/claude-sessions-monitor/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Buy Me a Coffee](https://img.shields.io/badge/Buy%20Me%20a%20Coffee-support-FFDD00?logo=buymeacoffee&logoColor=black)](https://buymeacoffee.com/yepzdk)

## Features

- **Live dashboard** showing all active sessions from Claude Code and Oh My Pi in one list
- **Both agents, auto-detected** — no flag to set. csm reads `~/.claude/projects/` and `~/.omp/agent/sessions/`, and skips whichever it doesn't find. When both are on screen the origin column carries a `[cc]` / `[omp]` badge after the origin name
- **Web dashboard** with `--web` flag for rich session inspection in the browser
- **History view** to browse past sessions with activity summaries
- **Process detection** distinguishes running vs inactive sessions
- **Ghost detection** identifies orphaned agent processes — ones whose launching shell or IDE has exited and whose log has been silent for over an hour. A session left open and idle in a live tab is not a ghost. Before signalling, csm re-checks that the process still belongs to that session's agent
- **Last message display** shows recent assistant responses
- **Git branch display** shows current branch for each session (Claude Code only — Oh My Pi does not record it)
- **Status indicators**: Working, Needs Input, Waiting
- **Usage and history views** with API quota bars and per-session token breakdown (press `u`). Both are labelled *(Claude Code)*: the quota comes from Anthropic's OAuth endpoint and both read Claude Code's logs, so Oh My Pi sessions do not appear in them
- **Jump to a session** — select a row with `↑`/`↓` and press `Enter` to bring its terminal tab to the front (macOS + Ghostty)
- **Origin column** showing what launched each session — a terminal (Ghostty, iTerm, Terminal.app, WezTerm, Kitty, Alacritty, Konsole, GNOME Terminal, ...), Claude Desktop, or an IDE (Zed, VS Code, Cursor, VSCodium, JetBrains) — detected from the agent process's parent chain + environment and cached to `~/.claude-monitor/origins/` so it survives session end. On a mixed dashboard it also carries the agent badge; the column widens to fit it, so a single-agent machine keeps exactly the columns it had
- **Session badges**: Agent [cc] / [omp], Unsandboxed [!S], Ghost [ghost], Incomplete data [?]
- **Single static binary** - no runtime dependencies, easy to install
- **Cross-platform** - macOS and Linux

## Installation

### Quick install (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/yepzdk/claude-sessions-monitor/main/install.sh | sh
```

Detects your OS and architecture, verifies the download against the release's
`checksums.txt`, and installs to `~/.local/bin/csm` — no sudo. Re-run it any
time to upgrade, or use [`csm -upgrade`](#upgrading).

Knobs: `CSM_VERSION` (default: latest) and `CSM_INSTALL_DIR` (default:
`~/.local/bin`). A version can also be passed as an argument, which is the form
that survives the pipe: `... | sh -s -- 0.8.0`.

### Homebrew (macOS/Linux)

```bash
brew tap yepzdk/tools
brew install csm
```

### From releases

Download the latest binary from [Releases](https://github.com/yepzdk/claude-sessions-monitor/releases) and add to your PATH.

**Linux (quick install):**

```bash
# amd64
curl -L https://github.com/yepzdk/claude-sessions-monitor/releases/latest/download/csm-linux-amd64 -o csm
chmod +x csm
sudo mv csm /usr/local/bin/

# arm64
curl -L https://github.com/yepzdk/claude-sessions-monitor/releases/latest/download/csm-linux-arm64 -o csm
chmod +x csm
sudo mv csm /usr/local/bin/
```

**macOS:** Use Homebrew (above) or download `csm-darwin-amd64` / `csm-darwin-arm64` from releases.

### Arch Linux (AUR)

```bash
yay -S csm-bin        # or paru -S csm-bin
```

`csm-bin` installs the released binary to `/usr/bin/csm`, so `yay -Syu` upgrades
it with the rest of your system.

### Debian / Ubuntu (.deb)

Grab the `.deb` matching your architecture from the [latest release](https://github.com/yepzdk/claude-sessions-monitor/releases/latest) and install with `dpkg`. The asset filename is `csm_<version>_<arch>.deb`:

```bash
# replace X.Y.Z with the release version, e.g. 0.6.0
VERSION=X.Y.Z
ARCH=amd64   # or arm64
curl -LO "https://github.com/yepzdk/claude-sessions-monitor/releases/download/v${VERSION}/csm_${VERSION}_${ARCH}.deb"
sudo dpkg -i "csm_${VERSION}_${ARCH}.deb"
```

### Fedora / RHEL (.rpm)

Asset filename is `csm-<version>.<arch>.rpm` (arch is `x86_64` or `aarch64`):

```bash
VERSION=X.Y.Z
ARCH=x86_64   # or aarch64
sudo rpm -i "https://github.com/yepzdk/claude-sessions-monitor/releases/download/v${VERSION}/csm-${VERSION}.${ARCH}.rpm"
```

### Go toolchain

```bash
go install github.com/yepzdk/claude-sessions-monitor@latest
```

This installs the binary as `claude-sessions-monitor` (the module name) into
`$GOBIN` or `~/go/bin`; rename or alias it to `csm` if you like.

### Build from source

```bash
git clone https://github.com/yepzdk/claude-sessions-monitor.git
cd claude-sessions-monitor
make install
```

## Upgrading

```bash
csm -upgrade
```

Checks GitHub for a newer release. If csm was installed with a package manager
(Homebrew, mise, `.deb`/`.rpm`, pacman, `go install`), it prints that tool's
upgrade command rather than overwriting a file the manager believes it owns.
Otherwise it downloads the new binary, verifies it against the release's
`checksums.txt`, checks that it runs, and replaces itself — a failure at any
step leaves the working csm untouched.

The live dashboard also checks once a day, in the background, and shows a
footer line when a newer release exists. To turn that off, set
`CSM_NO_UPDATE_CHECK` to **any** non-empty value — including `0`:

```bash
export CSM_NO_UPDATE_CHECK=1
```

## Usage

```bash
# Live view (default)
csm

# Live view with web dashboard
csm --web

# Web dashboard on custom port
csm --web --port 3000

# Web dashboard only (headless, no terminal UI)
csm --web-only

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
| `↑` / `↓` | Select a session row |
| `Enter` | Bring the selected session's terminal tab to the front |
| `h` | Switch to history view |
| `l` | Switch to live view |
| `u` | Switch to usage view (API quota + token breakdown) |
| `w` | Open web dashboard in browser (when `--web` is active) |
| `f` | Cycle the agent filter: all → Claude Code → Oh My Pi (only offered when both are present) |
| `Ctrl+C` | Quit |

#### Jumping to a session

`Enter` focuses the terminal tab running the selected session, leaving csm where it is.
This currently works on **macOS with Ghostty**; other terminals report that they can't be
focused rather than doing nothing. The first jump triggers the standard macOS Automation
consent dialog — allow it once and it won't ask again.

Sessions are matched to tabs by working directory, so if a project has both a Claude tab
and a plain shell open, csm picks the Claude one and notes that it guessed. Once Ghostty
ships [#11922](https://github.com/ghostty-org/ghostty/pull/11922) (merged upstream,
unreleased at the time of writing) csm matches on the exact tty instead, and the guess
disappears — no configuration needed.

### Watching both agents

Discovery needs no configuration: csm looks for `~/.claude/projects/` and
`~/.omp/agent/sessions/` and reports whichever exist. Both kinds of session land
in one list, sorted by who needs you soonest. When both are on screen the origin
column carries a `[cc]` or `[omp]` badge after the origin name — the origin is
what the column is about, and the agent qualifies it; both answer the same
question about where a session came from. On a terminal too narrow for the
origin column the badge moves into the project column instead of disappearing.

Press `f` in the live view to cycle which agent's rows you are reading: all →
Claude Code → Oh My Pi. It is a reading aid, not a discovery switch — both
agents are always scanned, so a filter can never hide a session csm failed to
find, and it resets when csm restarts. There is no flag for it: tracking every
agent on the machine is the point, and the web dashboard has no key to press to
undo a filter you forgot you set.

Oh My Pi relocates its session store for `--profile` and `--session-dir`. Point
csm at a non-default store with:

```bash
export CSM_OMP_SESSIONS_DIR=~/.omp-work/agent/sessions
```

Two columns stay blank for Oh My Pi rows, on purpose: **context %** (it is
multi-provider, so a context window cannot be derived from the model id, and a
wrong percentage reads as a measurement) and **git branch** (not recorded in its
logs). The history and usage views are Claude Code only, and say so in their
headings.

### Usage view

Press `u` in the live dashboard to see token usage. The view is labelled
*(Claude Code)* because both of its sections are: Oh My Pi authenticates against
its own providers and keeps its costs in its own logs. It has two sections:

- **API Quota** — Shows your Anthropic plan's utilization (5-hour and 7-day windows, plus per-model breakdowns when available). Uses color-coded progress bars: green (<75%), yellow (75-89%), red (≥90%). Reads the OAuth token from the macOS Keychain or `~/.claude/.credentials.json` on Linux.
- **Local Usage** — Aggregates token counts (input, output, cache) from session log files within a 5-hour rolling window, broken down per session.

### Web dashboard

Start with `csm --web` to run the web dashboard alongside the terminal UI. The dashboard is available at `http://localhost:9847` by default.

Features:
- **Live sessions** with status indicators, context bars, and auto-refresh via SSE
- **Usage tab** with API quota bars and per-session token breakdown
- **History view** with search/filter and date grouping
- **Session detail panels** with metrics (token usage, tool breakdown, turn count) and full message timeline
- **Timeline filters** to show All, Assistant, or User messages
- REST API: `/api/sessions`, `/api/history`, `/api/usage`, `/api/quota`, `/api/claude-status`, `/api/sessions/timeline`, `/api/sessions/metrics`, and `/api/events` (SSE stream)
- Embedded in the binary via `go:embed` — no external files or build step needed

## Status Types

| Symbol | Status | Description |
|--------|--------|-------------|
| ● | Working | Session is actively processing |
| ▲ | Needs Input | Waiting for user to approve a tool use |
| ◉ | Waiting | Turn completed, waiting for next prompt |
| ◌ | Inactive | No agent process running (shown in history) |

## Screenshot

```
Coding Sessions

● Working: 1  ▲ Needs Input: 1  ◉ Waiting: 0

STATUS          PROJECT                                  ORIGIN           CONTEXT          LAST ACTIVITY
────────────────────────────────────────────────────────────────────────────────────────────────────────
● Working       myorg/api-server @main                   Ghostty [cc]     ███████░░░ 68%   Now
  Implementing auth middleware

▲ Needs Input   work/api-gateway "Rate limiting"         Ghostty [omp]    -                12s ago
  Using: bash

↑↓: select | Enter: jump | h: history | u: usage | f: filter | Ctrl+C: quit
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

csm reads two session stores — `~/.claude/projects/` for Claude Code and
`~/.omp/agent/sessions/` for Oh My Pi — and parses their JSONL logs to decide
what state each session is in. The two formats have almost nothing in common, so
each has its own reader and its own status rules, but both answer with the same
four statuses on the same time windows, which is what makes one mixed list worth
reading. A scan of running processes then tells live sessions from finished ones,
identifying each process from its full command line — Oh My Pi runs as
`bun .../omp`, so the command name alone says nothing.

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) goes into detail: the status rules, ghost detection, what csm reads and writes on disk and over the network, and how the packages fit together.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](.github/CONTRIBUTING.md) for
the development workflow, [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for a
map of the code, and [SECURITY.md](.github/SECURITY.md) for reporting
vulnerabilities.

## Support

If csm saves you time, you can [buy me a coffee](https://buymeacoffee.com/yepzdk).

## License

[MIT](LICENSE)
