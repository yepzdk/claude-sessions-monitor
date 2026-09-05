# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- An upgrade asks before it replaces the binary, naming the file it is about to overwrite. Enter declines; `-y` (or `--yes`) skips the question, and so does a non-interactive stdin, so scripts and CI do not hang on it

### Changed

- Watching an Oh My Pi session with a long log costs less CPU per refresh

### Fixed

- csm no longer re-parses a session's subagent logs on every refresh, so watching a session that runs agents costs less CPU
- Flags work on either side of the subcommand: `csm upgrade -v` prints the version instead of reporting a flag csm has as an unknown argument, and `csm -l upgrade` is refused as a contradiction rather than upgrading and dropping the `-l`

## [1.2.0] - 2026-09-04

### Added

- The API quota falls back to Oh My Pi's Anthropic credential when Claude Code's has expired. Claude Code only refreshes its token while it is running, so a day without it left the quota panel showing a 401; omp holds a credential for the same account and refreshes it on read. The panel names the credential it asked with
- `make deadcode` and a CI step running `golang.org/x/tools/cmd/deadcode` once per `GOOS`. It reports an exported function that nothing reaches, which golangci-lint's `unused` does not

### Changed

- The usage view's sections are labelled separately: the API quota is the whole Anthropic account's, so everything billing that plan is in it, while local usage is still Claude Code's logs alone
- The maintainer address in the `.deb`/`.rpm` and AUR package metadata is `jesper@yepz.dk`, the address for this project

### Fixed

- A failed quota request reports what Anthropic said ("OAuth access token has expired. Re-authenticate to continue.") instead of a bare "API returned 401 Unauthorized"
- An expired token is recognised before the request, so the panel says which token lapsed and how to refresh it rather than spending a doomed call to find out
- The web dashboard's quota widget no longer breaks itself on a machine that was never signed in to Claude Code. It matched an error string csm had stopped emitting, so the "no credential is normal" path was unreachable and the widget showed a permanent "quota unavailable"

## [1.1.0] - 2026-08-30

### Added

- `csm upgrade` and `csm update` do what `csm -upgrade` does. Arguments after the flags were silently discarded, so either of these started the dashboard instead of upgrading; an argument csm does not recognise is now an error rather than a different command

### Changed

- The `[cc]` / `[omp]` badge is decided from every session on the machine, bounded to agents seen in the last week, instead of from the rows each view happens to be showing. The live view draws only active sessions and the web dashboard only the last hour, so the badge — and the six columns the origin column grows by to hold it — used to appear and vanish as sessions went idle, and the three surfaces disagreed about when. A machine that has settled on one agent stops showing badges a week later

### Fixed

- Jumping to a session on Linux works under a terminal that runs one process for all its windows, which is how Omarchy launches Ghostty (`--gtk-single-instance=true`). The compositor reports the same pid for every window, so csm now picks the one whose title carries the session's own title — the agent writes it there, which makes it per-window evidence the pid is not. Previously a desktop with more than one agent window open was reported as ambiguous and nothing was focused
- `f` no longer strands the live view in a filter. The key was only accepted while more than one agent was active, but the filter it set outlived that: filter to one agent, let its sessions go idle, and the rows stayed hidden with the footer no longer naming the key that would bring them back — restarting csm was the only way out. `f` now works whenever a filter is set, and an empty list says which filter emptied it instead of reporting "No active sessions."

## [1.0.0] - 2026-08-30

### Added

- Jumping to a session works on Linux under Hyprland, sway, and X11 window managers (X11 needs `wmctrl`, which is not installed by default). `Enter` raises the session's window, not its tab. GNOME and KDE under Wayland are not supported, and say so instead of doing nothing
- `install.sh`, a one-line installer (`curl -fsSL .../install.sh | sh`) that detects OS/arch, verifies the download, and installs to `~/.local/bin` without sudo
- `csm -upgrade` replaces a directly-installed csm in place; installs owned by Homebrew, mise, dpkg, rpm, pacman or `go install` get that tool's upgrade command instead
- The live dashboard checks for a newer release once a day in the background; set `CSM_NO_UPDATE_CHECK` to disable
- An AUR package, `csm-bin`, published automatically on release
- Releases now ship a `checksums.txt` asset, which `install.sh`, `csm -upgrade` and the Homebrew formula all verify against
- Oh My Pi sessions appear alongside Claude Code sessions in one list, auto-detected from `~/.omp/agent/sessions/` with no flag to set. When both are on screen the origin column carries a `[cc]` / `[omp]` badge
- `f` in the live view cycles which agent's rows are shown: all, Claude Code, Oh My Pi. A reading aid over an always-complete scan, not a discovery switch
- `CSM_OMP_SESSIONS_DIR` points csm at a non-default Oh My Pi session store, for `--profile` and `--session-dir` setups
- `harness` in the session JSON (`csm -l -json` and the dashboard's `/api/sessions`), naming which coding agent a session belongs to

### Changed

- The scan for running agent processes reads the kernel's process table directly (`/proc` on Linux, `sysctl kern.proc.all` on macOS) instead of parsing `ps` output. On Linux csm no longer spawns any subprocess except `xdg-open`
- Platform-specific code (`getProcessCwd`, `GetOAuthToken`, `openBrowser`) moved from `runtime.GOOS` switches to build-tagged files, so an unsupported platform is a build error rather than a silent no-op
- A process is identified from its full argument vector, read from the kernel (`/proc/<pid>/cmdline`, `kern.procargs2`), instead of from the truncated name the OS accounts it under. Session discovery and the `--kill-ghosts` recheck now apply the same rule, so a process one of them finds cannot be silently rejected by the other
- csm is now the *Coding* Sessions Monitor: it watches more than Claude Code. Same binary, same install, nothing to migrate
- The history and usage views are labelled *(Claude Code)*. Both read Claude Code's logs and the quota comes from Anthropic's OAuth endpoint, so Oh My Pi sessions do not appear in them

### Fixed

- On macOS, a session whose working directory contains a space was dropped from the dashboard: the path was read as the last space-separated field of `lsof` output
- A scan that found Claude processes but could not resolve any of their working directories (macOS without `lsof` on `PATH`) reported "no running sessions" with no error. It now reports the failure
- The usage view said "OAuth token not found" for every credential failure. It now reports the actual reason, and rejects credentials with an empty access token instead of sending an empty `Bearer` header
- Pressing `w` said nothing when the browser could not be launched (e.g. Linux without `xdg-utils`). The live view now confirms the launch or shows the error
- The amd64 release binary no longer requires a glibc as new as the CI runner's; release builds are now statically linked (`CGO_ENABLED=0`)
- `--kill-ghosts` printed "No processes were terminated (they may have already exited)" for a ghost it had listed and then declined to signal. It now names what the pid belongs to instead, so a refusal cannot read as a clean run
- The `--kill-ghosts` recheck accepted any process whose name merely ended in "claude", so a recycled pid belonging to something like `wrap-claude` could be signalled on a session's behalf
- A session whose Claude Code is installed under a version-named path (`~/.local/share/claude/versions/<version>`, which the native installer uses) was reported Inactive while it ran, and could never be found as a ghost: the process scan filtered on a name the kernel takes from the executable, and that name is the version
- A log that could only be read partway now keeps its `[?]` incomplete-data marker on every refresh. The marker previously appeared only on the tick that parsed the file and vanished while the parse was served from cache

## [0.7.0] - 2026-08-28

### Added

- `golangci-lint` runs in CI and in `make check`, once for `GOOS=linux` and once for `GOOS=darwin` so the macOS-only jump code is covered
- `docs/ARCHITECTURE.md`, a contributor guide to the data flow, status rules, ghost detection, caches, platform code and test helpers, plus package documentation pointing at it
- `go install github.com/yepzdk/claude-sessions-monitor@latest` now works, and `csm -v` reports the module version for such builds
- `pid_confident` in the session JSON (`csm -l -json` and the dashboard's `/api/sessions`), which says whether `ghost_pid` is known to be that session's own process or only a positional guess

### Changed

- The terminal size comes from `golang.org/x/term`, so `internal/ui/terminal.go` no longer depends on `syscall` and `unsafe`
- The Go module path is `github.com/yepzdk/claude-sessions-monitor`, matching the repository; it previously pointed at an organisation the project no longer lives under
- CI runs on macOS as well as Linux, so the darwin-only jump and origin-detection code is compiled and tested on every pull request

### Fixed

- The origin store directory is created `0700` rather than `0755`, and an existing one is repaired on the next save. Other accounts could list the session ids it held; the files themselves were already `0600`
- CONTRIBUTING.md described a release-on-merge flow, a Go 1.21 minimum and a standard-library-only rule, none of which had been true for some time
- In a project directory running more than one Claude session, csm paired sessions to processes by position — newest log to first `ps` result — so the process it named for a session was usually a different session's. Sessions are now matched by session id through the registry Claude Code keeps at `~/.claude/sessions/`, which also lets a session that has exited be reported as inactive instead of inheriting a neighbour's process. Only processes `ps` confirms are running are used, and Claude Code versions that write no registry keep the previous behaviour.
- The `[ghost]` badge and `--kill-ghosts` treated any session whose log had been silent for an hour as orphaned, so tabs left open overnight and sessions in their first minutes (before Claude Code creates the log) were flagged. A ghost now also has to have lost its parent process

## [0.6.0] - 2026-08-26

### Added

- CI, release, license and Buy Me a Coffee badges plus a support section in the README.

### Security

- The web dashboard rejects requests whose `Host` header is not a loopback name. Binding to `localhost` kept the dashboard off the network but did not stop DNS rebinding: a page the user merely visited could point its own domain at `127.0.0.1`, and the browser would then treat a fetch to the dashboard as same-origin. Nothing checked `Host`, so `/api/history` and every session timeline behind it — prompt text, file paths, anything pasted into a session — were readable
- The HTTP server now sets read, header and idle timeouts, so a connection trickling in one header byte at a time can no longer hold a goroutine and a file descriptor indefinitely

### Fixed

- `--kill-ghosts` could terminate a session that was actively working. Logs are sorted newest-first and pids arrive in `ps` order, and the two were paired by index, so in a project running several Claude processes the stale log could carry the busy process's pid. Ghost reporting now honours the same `PIDConfident` check that jump already used, and reports which processes refused the signal rather than only counting the ones that accepted it
- A running session whose log could not be read vanished from the dashboard and from the summary counts, because the read failure left it marked Inactive and inactive sessions are filtered out. Such rows now stay visible, marked `[?]` to show their numbers are incomplete
- `csm` reported "No active Claude sessions." with complete confidence when the `ps` scan itself failed, and the live view discarded discovery errors on every frame. Both now say what went wrong
- Token totals silently understated usage when a log contained a line past the scanner's size cap: the partial sum was returned as though the scan had reached the end of the file. The usage view now marks such totals as a lower bound
- "No token usage in the past 5 hours." is no longer printed when the search for that usage failed, in the web dashboard as well as the terminal
- The web dashboard marks a session whose log could not be read in full, matching the `[?]` the terminal shows, rather than presenting partial numbers as a reading
- A corrupt token count no longer wraps to a negative number and pulls the 5-hour total down
- A negative utilization value from the quota API no longer panics the usage view
- History date headings were a day out for everyone east of UTC, and labelled yesterday "Today" on the spring-forward day
- The web dashboard showed the context percentage against a 200K window for `claude-opus-5`, `claude-sonnet-5` and `claude-fable-5` while the terminal used 1M, because it re-derived the window from the model id with a regex that required a minor version segment. The window is now sent by the server
- The `[ghost]` badge could never appear: the flag behind it was always false, and orphaned sessions were filtered out of the live view before the badge could render
- A non-ASCII project name shifted every column to the right of it, because the column was padded by byte count after being truncated by rune
- `ReadKey` spun at 100% CPU forever when stdin closed, which happens when csm is started detached or loses its pty
- A session whose log could not be read to the end was listed in the history view as "0s, 0 msgs", and that zero was folded into the footer total. Such rows now carry the same `[?]` the live view uses, in the web dashboard as well as the terminal, and the total says it is a lower bound
- A panic while scanning sessions left the terminal in raw mode on the alternate screen: csm exited with echo off and an invisible shell prompt, and the stack trace was painted onto a screen the terminal then discarded. It still exits, but restores the terminal first and prints the trace where it can be read
- SSE connections leaked a goroutine each at shutdown, and the hub scanned the filesystem every two seconds even with no dashboard open. A failed scan now tells the dashboard its data is stale instead of leaving it showing frozen state under a "connected" indicator
- `-days` and `-port` printed their default twice in `--help`

### Changed

- `QuickSessionStats` returns a struct and an error instead of six positional values, three of them adjacent strings that could be swapped without the compiler noticing

### Removed

- `internal/watcher`, `findMostRecentLog`, `GetGhostPIDs` and the `StatusIdle` constant, none of which had callers

## [0.5.0] - 2026-08-23

### Added

- Web dashboard: page favicon
- Web dashboard: compact 5-hour/7-day API quota indicator in the header, visible on every tab. Polls every 5 minutes (paused when the tab is hidden) rather than the usual short interval, since the underlying endpoint is undocumented and reported to get stuck rate-limited for the rest of the session if polled too aggressively; the widget circuit-breaks on the first failed fetch (other than "no OAuth token configured") and shows "quota unavailable" instead of retrying or silently going stale
- Web dashboard: subagent rows now show a proper connected tree (a continuous line down to each subagent, hanging from the parent session card) instead of a repeated, disconnected `└` on every row
- `/api/quota` endpoint returning just the API quota, for callers that don't need the full local-usage log scan `/api/usage` also does
- API quota requests now identify csm with a `User-Agent` of `csm/<version> (+<repo url>)`, instead of going out unlabelled

### Changed

- Web dashboard: the session Metrics panel now leads with context usage as a figure and a meter, because it is the only metric there with a consequence. The volume counts follow as one line, where prompts and replies open the timeline filtered to those entries, and colour is reserved for state instead of marking card categories
- Web dashboard: the token breakdown is now one stacked bar of the session total, with a legend carrying each exact value and share, instead of four independent bars — which were log-scaled, so a negligible value could render with a bar nearly as long as one orders of magnitude larger. Its colours were re-picked: the previous green and yellow were indistinguishable to a reader with deuteranopia
- Web dashboard: the tool list is now ranked rows sharing one bar baseline, so the counts are comparable by length. The count sits beside the tool name rather than at the far edge of the panel, and MCP tool ids are split so the tool name leads and the server follows as a separate label

### Fixed

- Web dashboard: opening a session and switching to another before the first one's metrics arrive no longer renders the first session's numbers under the second one's title
- Web dashboard: the timeline's type filter is now applied server-side, so `Load more` pages through matching entries instead of raw ones. Filtering to User and clicking `Load more` often did nothing visible: user turns are a few percent of a session, so whole pages of raw entries contained none of them, and the button counted raw entries either way
- History prompt previews no longer show raw `<local-command-caveat>`/`<command-name>` scaffolding or literal `\n` escapes for sessions started via a slash or local command; the preview now shows the command itself, and JSON string escapes are properly decoded
- Web dashboard: undefined `--muted` CSS variable (three spots) now correctly resolves to `--text-muted`, so quoted session titles and the extended-context model badge render at their intended dimmed color instead of full brightness
- Web dashboard: dimmed text now meets WCAG AA (4.5:1) on every surface it actually renders on, not just the card background. `--text-dim`/`--text-muted` were as low as 2.76:1; the header's small type moved up a tier so an inactive tab reads at 6.43:1 instead of 5.28:1
- Web dashboard: stopped session cards no longer use a blanket `opacity`, which composited every row inside them against the page and left their text at 2.67:1 — below the contrast the rest of the palette guarantees. They now recede by sitting flat against the page background instead, and the text keeps its normal contrast
- Web dashboard: the embedded HTML/CSS/JS assets are now served with `Cache-Control: no-cache`; previously `embed.FS`'s zero mod-time could make browsers cache them indefinitely across `csm` upgrades
- Web dashboard: a subagent row with only a spawn label (no status message yet) no longer renders with an empty title line and the label stranded on its own second line

## [0.4.0] - 2026-08-21

### Added

- Community health files: MIT `LICENSE`, code of conduct, contributing guide, security policy, and issue/PR templates.
- CI workflow checking formatting, vet, build and tests on every pull request, plus `make fmt` / `make check` and an `.editorconfig` so style stays consistent and formatting churn stays out of feature diffs.
- Live subagents are now shown as nested rows under their parent session in both dashboards, and a parent with a running subagent no longer reports as idle. (#54)
- Jump to a session's terminal from the live view: select a row with `↑`/`↓` and press `Enter` to bring its Ghostty tab to the front. macOS and Ghostty only for now. (#48)
- Web dashboard: clicking the "User Prompts" metric card in the session detail modal now jumps to the Timeline tab with the `User` filter applied, scrolled to the first prompt
- Web dashboard: timeline "Load more" escalates after the second click — the third click loads all remaining entries in one go (chunked server-side at 500 per request) instead of forcing repeated clicks
- Active model id is now exposed on the session JSON/SSE API and indicated in both dashboards: the terminal shows a dim `(1M)` suffix on the context cell when the session is using an extended context window, and the web dashboard shows a small `1M` badge with the full model id on hover
- Native `.deb` and `.rpm` Linux packages published with each release (amd64 and arm64)
- Origin column showing where each session was launched from — terminal emulator (Ghostty, iTerm, Terminal.app, WezTerm, Kitty, Alacritty, Konsole, GNOME Terminal, ...), Claude Desktop, or IDE (Zed, VS Code, Cursor, VSCodium, JetBrains). Detection walks the Claude process's parent chain and inspects its environment; results are snapshotted to `~/.claude-monitor/origins/<sessionId>.json` so the badge persists after the session ends. The column is shown in both the terminal dashboard (drops out gracefully below 90 columns) and the web dashboard, and is also included in the JSON/SSE API responses.
- Linux process detection via `/proc/<pid>/cwd` — live session status now works correctly on Linux without requiring `lsof`
- Project naming from `cwd` field in JSONL logs — accurate, lossless project names on all platforms (replaces heuristic decoding of encoded directory names)
- Session title display — custom titles set by Claude Code are shown in both TUI and web dashboard
- Parse `cwd` and `customTitle` fields from Claude Code JSONL log entries
- Linux manual install instructions in README
- Path markers for project naming: `/repos/`, `/src/`, `/code/`, `/workspace/` in addition to `/Projects/`
- Parse `stop_reason` field from Claude Code JSONL logs for more accurate status detection
- Track `progress`, `hook_progress`, and `agent_progress` log entries as activity heartbeats
- Detect multiple concurrent Claude sessions in the same project directory (each shown as a separate row/card)
- Show Claude service status from status.claude.com in terminal live view and web dashboard
- `--web-only` flag for headless web server mode (no terminal UI required)

### Changed

- Releases are now cut by pushing a tag rather than by every merge to `main`, so the version reflects a deliberate decision instead of counting merges. Fixed the tag-triggered workflow's Homebrew step, which had been firing a no-op `repository_dispatch` at the tap since the release consolidation.
- CI and release workflows now run on Node 24 actions (`checkout@v7`, `setup-go@v7`, `action-gh-release@v3`); the release job's Go version now matches `go.mod` instead of claiming 1.21.
- Release automation now updates the Homebrew formula directly from the release workflow instead of via a second workflow in the tap repo, so only one token needs to be kept current.
- Terminal: Claude service status is fetched on-demand (startup + key press) instead of every ticker cycle
- Web: Claude service status polling pauses when the browser tab is hidden and resumes on visibility
- Make usage/quota fetching fully on-demand instead of periodic polling
- Terminal: usage data fetched only on view entry (`u`) or manual refresh (`r`)
- Web: usage data fetched via REST on tab switch or refresh button click, no longer broadcast via SSE
- Increase API quota cache TTL from 30s to 60s to reduce Anthropic API request frequency

### Fixed

- History prompt previews no longer end in a broken character when the prompt contains non-ASCII text. The 120-character preview limit now counts characters rather than bytes, so previews are the same length in every language instead of being cut short in Danish, German, CJK and similar. (#68)
- Sessions in a home directory containing characters other than letters, digits or dashes (e.g. an `@`) no longer all report as Inactive. (#53)
- A genuinely active session that goes quiet for a while (extended thinking, a long tool call) no longer vanishes from the dashboard or reports as Inactive. (#58)
- A single unreadable log line no longer discards everything already parsed from that session's log. (#57)
- Working sessions no longer swap rows between refreshes when they all display the same activity text. (#56)
- The live view no longer repeats or flickers in block-based terminals; it now uses the alternate screen buffer and restores prior scrollback on exit. (#55)
- Context usage is no longer overstated ~5x for Claude 5 family models (`claude-fable-5`, `claude-sonnet-5`): their two-part model ids now parse correctly and map to the 1M context window. (#51)
- Sessions no longer stay stuck on "Working" after Claude has yielded back to the user; idle sessions now age out to "Waiting" with the real last message.
- Sharply reduced CPU usage of the live view (previously ~40-50% at idle) by caching session log parsing.
- Sessions no longer show false "Working" status after a turn completes — turn-completed detection (`turn_duration` and `stop_reason: "end_turn"`) now takes priority over file modification time and progress heartbeat checks
- Session title and cwd now extracted via full-file scan (`QuickSessionStats`) for both active and history views, ensuring consistency and finding titles set early in long sessions
- Project names on Linux `/home/<user>/` paths now skip the home prefix for cleaner display (e.g., `repos/myproject` instead of `home/user/repos/myproject`)
- UTF-8 safe truncation in TUI — branch names, session titles, and project names with multi-byte characters are no longer split mid-character
- Removed hardcoded `max-width: 200px` on session title in web dashboard; flexbox layout handles overflow responsively
- Project names on Linux paths (`/home/user/...`) now display correctly instead of falling back to an ugly slash-separated dump
- Port conflict error message now suggests `ss -tlnp` on Linux instead of macOS-only `lsof -i`
- Sessions actively using tools, hooks, or subagents no longer flicker to "Waiting" — progress heartbeats from Claude Code logs are now tracked
- Multi-tool_use detection: all tool calls in an assistant message are now checked, not just the first
- Extended assistant "Working" window from 30 seconds to 2 minutes to reduce false "Waiting" during brief log gaps
- Use log file modification time to detect active streaming writes, preventing "Waiting" during early response generation
- Context percentage now correctly recognises 1M-context models from Opus/Sonnet generation 4.6 onward (e.g. `claude-opus-4-7`, `claude-sonnet-4-7`, future generations); previously only `opus-4-6` and `sonnet-4-6` were detected, so newer models were treated as 200K and reported up to 5× too high
- Unhelpful error when starting csm while another instance is already running on the same port
- Include output tokens in context window calculation to match Claude Code's reported usage
- `--web` and `--web-only` flags now report an error when used together instead of silently ignoring `--web`

### Removed

- macOS menu bar app (`CSMMenuBar.app`) — the SwiftUI menu bar app, its build targets, release workflow, and Homebrew cask have been removed. Use the web dashboard (`csm --web`) for at-a-glance session monitoring instead. The `--web-only` headless mode remains available for running csm as a background server.
