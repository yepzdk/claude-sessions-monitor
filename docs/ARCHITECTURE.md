# Architecture

A map for contributors. It tells you where things live, what the non-obvious
rules are and why, and which helpers to reuse. It links to files, not line
numbers; the code carries a rationale comment at nearly every non-obvious
decision, and that comment is the authority if the two ever disagree.

## Data flow

```
~/.claude/projects/<encoded-cwd>/<session>.jsonl  ──►  discoverClaude()  ─┐
~/.omp/agent/sessions/<encoded-cwd>/<ts>_<id>.jsonl ─►  discoverOMP()  ───┤
                                                                          │
the OS process table  ──►  classifyProcess()  ──►  both above  ───────────┤
                                                    []session.Session  ◄──┘
                                                             │
                                    ┌────────────────────────┼────────────────────┐
                                 ui (TUI)            web (JSON + SSE)     jump (focus tab/window)
```

`internal/session` is the only package that reads logs or looks at processes.
`ui`, `web` and `jump` consume `[]Session` and never go back to the sources.
`session.DiscoverHistory(days)` is the parallel path for past sessions, and
`session.ParseTimeline` / `ParseMetrics` read a single log on demand for the
web detail panel.

There are two session producers behind `Discover()`: the Claude Code path in
`session.go`, and the Oh My Pi path in `omp.go`. They are two concrete
functions, not an interface with two implementations — the two log formats share
nothing but the letters JSONL, so an interface would have one method per caller
and no reuse. What *is* shared they call directly: the `ps` scan, `getProcessCwd`,
`findActiveLogs`, the caches, `sessionLess`, and origin detection.

Every `Session` carries a `Harness` (`claude` / `omp`), serialized as `harness`.
It is not decoration: `--kill-ghosts` verifies a pid against it before sending
SIGTERM, and `ui` decides the row tag from it.

`main.go` dispatches on flags in this order: `-v`, `-kill-ghosts`, `-history`,
`-l` (with `-json`), `-web-only`, then the default live view. The harness filter
is view state owned by the live loop and cycled with `f`, deliberately not a
flag: discovery always covers every agent, and a flag would have had to mean
something for the web dashboard, which serves several clients and has no key to
undo it. The live loop also owns the `ViewMode` (live / history / usage); `ui`
only exposes the three renderers.
The ticker runs at `-interval` (2s); the usage view never
auto-refreshes and the history view is throttled to once per 30s.

## `internal/session`

### The process scan (`harness.go`, `session.go`)

`getRunningHarnessProcs()` returns one `harnessProcess` per recognised agent,
with its harness, cwd, controlling terminal and whether it has been orphaned. It
reads the OS process table (`listProcesses`, see
[Platform code](#platform-code)) — not `ps` output, whose columns are a text
table meant for people and are not stable across platforms.

Three reads, narrowing: `comm` from the table is the loose prefilter
(`harnessCandidate`), the full argument vector decides
(`classifyProcess(argv)`), and only then are cwd and controlling terminal
resolved per pid. `comm` alone cannot decide — omp runs as `bun /path/to/omp`,
so its `comm` is `bun`, and Claude Code's native installer produces a `comm` of
the *version number* — and argv costs one read per pid, so the order matters.

The controlling terminal is read from the process's **stdin**
(`processTerminal`), because that is the descriptor omp itself calls
`ttyname(3)` on to name its session breadcrumb. Reassembling a name from a
device number would be a name csm then has to hope matches. Only omp pairing
uses it, so it is a per-candidate read rather than a column on `procInfo`.

A failed scan aborts `Discover` with an error rather than returning an empty
slice: empty is indistinguishable from "nothing running" and would mark every
session Inactive. An empty table with no error is rejected for the same reason.

### Claude Code discovery (`session.go`)

1. `ClaudeProjectsDir()` is `$HOME/.claude/projects`. Every subdirectory is a
   project; its name is Claude Code's encoding of the working directory
   (`encodeProjectPath`: every rune outside `[A-Za-z0-9-]` becomes `-`).
2. `pidsByDir(procs, HarnessClaude, encodeProjectPath)` keys the scan the same
   way, so joining processes to project directories is a plain string-key match.
3. `findActiveLogs(dir, runningCount)` picks which `*.jsonl` files to parse.
   `agent-*.jsonl` (subagent logs) are skipped. With no running process it
   returns the single newest log; with N processes it returns the N newest plus
   any log modified in the last 30 minutes, so a quiet-but-live session cannot
   lose the recency race to an unrelated file and vanish.
4. `parseSession` → `parseLogFile` → `applyParsedLog` → `determineStatus`.
5. Results are sorted by status priority (Working, Needs Input, Waiting,
   Inactive) then recency; two Working sessions sort by name so they don't
   swap places every frame.

The encoded directory name is only a fallback for the project name. The real
working directory is the `cwd` field inside the log, and `extractProjectName`
(`history.go`) turns it into the short `org/repo` label.

### Oh My Pi discovery (`omp.go`, `omp_terminal.go`)

`ompSessionsDir()` is `$HOME/.omp/agent/sessions`, overridable with
`CSM_OMP_SESSIONS_DIR` — omp relocates its store for `--profile` and
`--session-dir`, so unlike Claude Code the default path is not a guarantee.

omp's bucket encoding is home-relative, has had several spellings, and is
migrated best-effort on access, so csm does not reimplement it. Instead the
working directory is read from the newest log's header through the parse cache
(one `listLogsByRecency` per bucket, no second scan) and joined to processes
keyed by raw cwd. `selectActiveLogs` is shared with the Claude path; it already
skips directories, which is what excludes omp's per-session artifact directory
sitting beside each log.

What the format demands of the parser:

- The physical first line is a fixed-width 256-byte `{"type":"title"}` slot, not
  the header. A parser that assumed otherwise reads no `cwd` and every omp
  session loses its project name and its process. The slot also wins over the
  header's `title`: it is the copy omp rewrites on rename.
- Entries form an append-only **tree** (`id`/`parentId` + leaf pointer). csm
  reads the physical tail. Known ceiling: after a rewind or branch switch the
  tail may not be on the live branch. It is what the file's mtime reflects, and
  walking from the leaf would mean reading whole multi-MB files every tick.
- `message.content` is either a block array or a bare string, and `custom.data`'s
  shape depends on `customType`, so both arrive as `json.RawMessage`.
  `decodeOMPEntry` decodes the parts csm reads once, at parse time, and drops the
  raw copies — the status rules run on every tick including cache hits.
- An unparseable line costs one entry, not the session. omp's own loader is
  lenient here too.

Pairing has no registry to work from — omp writes no pid anywhere. It does write
a best-effort breadcrumb per terminal (`terminal-sessions/<terminal-id>` →
cwd + log path), named after `ttyname(3)` of its stdin with the `/dev/` prefix
dropped and the separators replaced. `ompTerminalID` applies the same
transformation to the terminal the scan read, so `pairOMPProcess` can match them
on both platforms — comparing raw paths matched on macOS and never on Linux,
where `/dev/pts/3` is `pts-3` on disk. It degrades to running-with-no-pid
(`PIDConfident` false) when omp used an env-derived id (`TMUX_PANE`,
`CMUX_SURFACE_ID`, ...) that no descriptor exposes, or the process has no
terminal. Two breadcrumbs naming one log drop the pairing rather than guess, the
same rule `pairProcess` applies to a duplicated session id. When every process in
a directory is confidently claimed by *another* log's breadcrumb, this log is
reported not-running rather than falling back to the generous bias — the same
"every pid accounted for" rule the registry path uses.

Known ceiling: an omp store that exists but cannot be read is skipped silently.
Failing the sweep would take the Claude Code half of the dashboard down with it.
The honest fix is a partial-scan warning surface; `sse.go` already has
`scan_error` to hang it on.

### Status inference (`determineStatus` in `session.go`)

Reads four kinds of log entries: `user`, `assistant`, `system` with subtype
`turn_duration`, and the progress heartbeats (`progress`, `hook_progress`,
`agent_progress`).

Three named windows, shared with the omp rules so that the status column means
the same thing on every row of a mixed dashboard: `recentActivityWindow` (2m,
"recent activity"), `logWriteWindow` (30s, the log file's mtime),
`sessionStaleWindow` (5m, "the whole session is stale").

The rules, in evaluation order:

- Not running → **Inactive**. Running with an empty log → **Waiting** (just
  started).
- The last assistant message has more `tool_use` blocks than the following
  user message has `tool_result` blocks → a tool is pending. Younger than 2m →
  **Working** (`Using: <tool>`); older → **Needs Input**. This is the *only*
  producer of Needs Input.
- A `turn_duration` entry newer than the last assistant message means the turn
  finished → **Waiting**, unless a newer user prompt is within 2m (→ Working).
- `stop_reason == "end_turn"` with no newer prompt → **Waiting**.
- Progress heartbeat within 2m, or file mtime within 30s → **Working**.
- Nothing at all within 5m → **Waiting**.
- Assistant message or a genuine user prompt within 2m → **Working**.
- Otherwise **Waiting**.

A user entry that carries only `tool_result` blocks is *not* a prompt
(`isUserPrompt`); it is the tail of Claude's own turn. Live subagents
(`subagent.go`) promote Waiting to Working but never override Needs Input.

### Status inference (`determineOMPStatus` in `omp.go`)

Same four statuses, same three windows, different evidence. omp has no progress
heartbeats, but it announces every tool call with a `tool_execution_start` custom
entry, and a working session calls tools.

The rules, in evaluation order:

- Not running → **Inactive**. Running with no entries → **Waiting** (just
  started; omp keeps a new session in memory until its first assistant message,
  so this window is wider than Claude Code's).
- A tool call announced with no matching result → younger than 2m → **Working**,
  older → **Needs Input**. The *only* producer of Needs Input.
- A `session_exit` newer than the last assistant message → **Waiting**.
- Last assistant message with `stopReason` `stop` or `aborted` and no newer
  prompt → **Waiting**. This is checked before mtime, because the write that
  ends a turn updates the mtime and would otherwise read as Working.
- File mtime within 30s → **Working**. Nothing within 5m → **Waiting**.
- Assistant message or user prompt within 2m → **Working**.
- Otherwise **Waiting**.

Pending tool calls are an exact **set difference**: `tool_execution_start` and
the `toolResult` message both carry the same `toolCallId`. Claude Code only
allows a count of `tool_use` against `tool_result` blocks, which cannot say
*which* call is outstanding. The task label prefers the start entry's `intent`
(a one-line description omp already wrote) over the bare tool name.

A `user` role is always a real prompt here — omp gives tool results their own
`toolResult` role — so there is no `isUserPrompt` equivalent to write.

Fields deliberately left zero on an omp session (`applyOMPParsedLog`):
`ContextPercent`, `ContextTokens`, `ContextWindow`, `GitBranch`,
`HasUnsandboxed`, `Subagents`. omp is multi-provider, so a context window cannot
be derived from the model id the way `contextWindowForModel` does for Claude, and
a wrong percentage reads as a measurement. The rest have no equivalent in its
log. If you add a column, blank it honestly rather than guessing.

### Processes, ghosts and the pairing pitfall

A process is identified in two steps, in `harness.go`. `harnessCandidate`
filters the process table on `comm` — cheap, already in hand, and deliberately
loose, because missing an agent here means never reading the argv that would
have identified it. `classifyProcess` then decides from the full argv, read
from `/proc/<pid>/cmdline` or `kern.procargs2`, and returns a `Harness`
(`claude`, `omp`, or none). It matches the basename of a whole argv element,
never a substring: `~/.omp/puppeteer/.../Google Chrome for Testing` and
`Python -u /var/.../omp-python-runner/runner.py` both contain "omp" and are a
browser and a REPL. Taking argv as the kernel stored it, rather than
re-splitting a printed command line, is what keeps `/Volumes/My Disk/bin/claude`
one element. Discovery and the pre-`SIGTERM` recheck call the same function, so
a pid one lists can only fail the other by having been recycled. `Session` and
`GhostProcess` both carry the harness, serialized as `harness`.

Claude Code (2.1.x) keeps a registry at `~/.claude/sessions/<pid>.json` with
the pid, session id and cwd of every live session; `registry.go` reads it.
Every entry is validated against the set of `claude` pids the scan found,
never against the bare pid — because a crash or reboot leaves files behind
and the pid gets reused. `pairProcess` then decides, per log: registry hit →
that session's own pid, confident; registry present and every pid in the
directory accounted for → not running; a pid the registry cannot name →
running, no pid. Two files carrying one session id (`--resume` in two tabs)
drop the id rather than guess. The registry snapshot is taken under the same
lock and TTL as the process scan so the two views cannot disagree mid-tick.

Without a registry (older Claude Code) the pairing is positional: logs are
sorted newest-first, pids arrive in scan order, the two are unrelated, and
pairing log *i* with pid *i* is a guess. Everything downstream is built to
survive that:

- `isRunning = len(pids) > 0` — any claude process in the project directory
  marks every candidate log as running. Deliberately generous: a wrong
  "running" self-corrects to Waiting through content staleness, whereas a
  wrong "inactive" hides a live session from the dashboard entirely.
- `Session.PIDConfident` is true on a registry hit, or when the directory has
  exactly one log and exactly one process. It is serialized as
  `pid_confident` so API consumers can tell a real pid from a guess.
- `IsGhost = running && parent pid is 1 && LastActivity older than GhostThreshold (1h)`,
  computed in `applyParsedLog` because it needs `LastActivity`. Orphaning is
  the signal; the hour of silence protects the one legitimate orphan, a
  headless `claude -p` whose shell has exited. Known ceiling: a Linux
  subreaper adopts orphans instead of pid 1 and this misses them.
- `ghostsFrom` refuses any session that is not `PIDConfident`, and
  `KillGhostProcesses` re-checks, through `verifyGhostProcess`, that the pid
  still belongs to *the session's own* harness before `SIGTERM` — not merely to
  some coding agent, which would accept a recycled pid that now belongs to the
  other one. A session with no harness can never name a process to kill. An
  unconfident pairing would be a coin flip over which session dies. `jump` is
  deliberately laxer — it reports a guess rather than declining, because a
  wrongly raised window costs a keystroke and a wrongly killed session costs
  work.
- A ghost that fails that recheck for any reason but having exited is reported
  as a failure rather than dropped, so "csm listed it and refused to signal it"
  cannot read as "it had already exited".

The omp side has no registry at all; see
[Oh My Pi discovery](#oh-my-pi-discovery-ompgo-omp_terminalgo) for the
breadcrumb pairing that replaces it.

### `Degraded`

`Session.Degraded` (and `HistorySession.Degraded`) is non-empty when a log
could not be read to the end — typically a line over the 10 MB scanner cap or
an I/O error. Partial data is kept unless zero entries parsed
(`isFatalParseError` in `cache.go`); the session still gets a status and stays
visible. The non-fatal error is cached alongside the partial parse, so the marker
stays put instead of showing only on the tick that parsed the file. Every view
must render it so the numbers are not read as measurements: `[?]` in `ui/ui.go`
and `ui/history.go`, the `?` badge in `web/static/app.js`. A new view or column
that shows counts needs the same.

### Caches (`cache.go`, `quota.go`, `status.go`)

| Cache | Key / TTL | Why |
|---|---|---|
| `parseCache` / `ompParseCache` | log path; valid while `(mtime, size)` unchanged | skip re-parsing multi-MB logs every tick. One generic `cachedParse[T]` policy, two maps, because the formats parse into different shapes |
| `processScanCache` | 2s | one read of the process table per tick, not per caller. Guarded by `processScanValid`, not a nil check: no agent running is a legitimate result |
| `resultCache` | 1s | TUI loop, SSE hub and HTTP handlers collapse to one scan |
| `apiQuotaCache` | 60s | the quota endpoint rate-limits hard |
| `claudeStatusCache` | 60s | status page, fetched on demand only |

All are package state. Tests that go through `Discover()` must reset them —
see [Writing tests](#writing-tests).

### What csm touches outside its own process

Reads: `~/.claude/projects/` (logs, `sessions-index.json`),
`~/.omp/agent/sessions/` (logs) and `~/.omp/agent/terminal-sessions/`
(breadcrumbs), and the OAuth token from the macOS Keychain item
`Claude Code-credentials` or `~/.claude/.credentials.json` on Linux
(`oauth.go`).

Nothing else under `~/.omp` is read, deliberately. `agent/models.yml` holds the
authoritative context windows csm would like for its context column — and
provider API keys in plaintext right beside them. `models.db` has the same
numbers behind a SQLite dependency that would end the single static binary. The
context column stays blank for omp rows instead.

Writes: only `~/.claude-monitor/origins/<sessionID>.json` (`origin_store.go`),
atomically via temp file + rename, so a session's origin badge survives after
its process is gone. Nothing else on disk.

Network (`http.Client` with 5s timeout, both):
- `https://api.anthropic.com/api/oauth/usage` — the quota view. This is an
  undocumented endpoint. The `User-Agent` is `csm/<version>
  (+https://github.com/yepzdk/claude-sessions-monitor)` on purpose: honest and
  greppable rather than imitating another client. Keep it that way.
- `https://status.claude.com/api/v2/status.json` — service health.

Subprocesses, all macOS-only bar the last: `lsof` (a process's cwd), `ps`
(origin detection, and the tty lookup in jump), `security` (Keychain) and
`osascript` (jump). The native calls for the first and last are libproc and
Security.framework, both cgo, and the release workflow cross-builds the darwin
targets from a Linux runner in one job. On Linux csm spawns `xdg-open` and,
only while jumping, the one window-manager client its display server calls for
(`hyprctl`, `swaymsg` or `wmctrl`).

### Platform code

Build tags, one file per OS with identical function signatures. That is the
default here: with a `runtime.GOOS` switch every branch must compile on every
platform, so an unhandled OS is only found at runtime, while a missing
build-tagged file is a build error.

- `session/listprocs_linux.go` / `listprocs_darwin.go`: the process table and
  one pid's name and cwd. Linux reads `/proc`; macOS calls `sysctl kern.proc.all`
  and spawns `lsof` for the cwd. The `getProcessCwd` comment there says why.
- `session/origin_detect_darwin.go` / `origin_detect_linux.go`: read a
  process's environment and parent chain (`ps -E` vs `/proc`). There is no
  windows file and no catch-all, so `GOOS=windows go build` fails here.
- `session/oauth_darwin.go` / `oauth_linux.go`: read the Claude Code OAuth
  token from the macOS Keychain or `~/.claude/.credentials.json`. Each reports
  why it failed, so a platform csm cannot read is not reported as "no token
  found".
- `browser.go` holds `openBrowser`; `browser_darwin.go` and `browser_linux.go`
  each supply only the `browserOpener` constant (`open`, `xdg-open`). Splitting
  a shared body from a per-OS constant beats copying the body once per OS.
- `jump/jump_darwin.go` / `jump_linux.go`: focus a terminal tab (macOS) or
  window (Linux). There is no stub for other systems because
  `origin_detect_*.go` already limits the build to these two.

`runtime.GOOS` appears once in the codebase: the port-conflict hint in
`web/server.go`, which picks between two format strings and is not dispatch.

There is no `origin_detect_*.go` for other systems, so `internal/session`
does not build on Windows or BSD. That is known; don't fix it in passing.

When you touch any of these, compile both halves:

```bash
GOOS=darwin go vet ./... && GOOS=linux go vet ./...
```

CI runs the full check on both `ubuntu-latest` and `macos-latest`, and
`make lint` runs golangci-lint once per `GOOS` for the same reason.
`jump/livecheck_manual_test.go` and `jump/livecheck_linux_manual_test.go` are
behind the `manual` build tag because they need a real terminal, live sessions,
and (on macOS) Automation consent; each file header says when and how to run
it. The Linux one also has `TestInspectLinux`, which dumps every window the
compositor reports next to the window each live session resolves to — start
there when a jump does not do what you expect.

### Context window

`Session.ContextWindow` is decided on the server (`contextWindowForModel`) and
sent to the frontend. Do not re-derive it from the model id in JavaScript —
that bug shipped once, and the two sides disagreed about the same session.
Rules: `claude-<family>-<major>[-<minor>]`; opus/sonnet/fable at ≥ 4.6 (or any
Claude 5) get 1M, everything else 200K. `extractContextUsage` reads the newest
assistant usage entry *after* the last `compact_boundary`, because compaction
resets the count.

## `internal/ui`

Rendering: alt screen, cursor home, then every row ends in `\033[K\r\n`
(erase to end of line). There is no clear-screen; frames overwrite in place,
which is what avoids flicker. Each frame is built in one `strings.Builder` and
written with a single `Print`. `newlineFor(interactive)` is how the history
and usage renderers serve both the one-shot CLI output and the live view.

Input: `golang.org/x/term` raw mode; `ReadKey` reads 16-byte chunks so a
3-byte arrow sequence isn't mistaken for a bare Escape, and exits for good on
read error (retrying spun at 100% CPU when stdin was gone). Arrow keys are
mapped into private-use runes (`KeyUp`, `KeyDown`) so the channel stays
`chan rune`.

Reuse these instead of writing new ones:

- `truncate` — rune-safe, appends `...`.
- `sanitizeForTerminal` — strips control characters. Apply it to *every*
  string that came from a log or the filesystem; otherwise a session title can
  inject ANSI into the dashboard.
- `ActiveSessions` — the one filter that decides which rows are shown. Any
  code that addresses a row by index (selection, jump) must use it too.
- `session.MixedHarnesses` / `FilterByHarness` — the agent label renders only in
  mixed company, and "mixed" is a question about the *machine*: every surface
  passes everything `Discover` returned, bounded by `HarnessBadgeHorizon` (7
  days). Reading it off the rows a view is about to draw is what it used to do,
  and it made the badge — and the six columns `calcSessionLayout` adds for it —
  come and go as sessions went idle, differently on each of the three surfaces:
  `-l` spans every session, the live view only active ones, the web dashboard
  active plus the last hour. The web dashboard cannot derive it at all, which is
  why the SSE `harnesses` event carries it (`harnessEvent`, sent immediately
  before the `sessions` frame it applies to).
- `f` is a display filter; both agents are always scanned. The filter outlives
  the mixed dashboard that allowed it, so the key and the footer entry are
  offered whenever `Mixed || Filter != ""` — gating them on `Mixed` alone let a
  user filter to one agent, watch it go idle, and end up with rows hidden and no
  key that would bring them back (`liveHelpKeys`, `emptyLiveMessage`).
- The agent badge lives in the origin cell (`formatOrigin`), one space after the
  origin name: which agent and what launched it are one fact about where a
  session came from, and the origin is the column's subject. It has to read as
  attached to the name, so all the slack goes to the right of the cell — padding
  the name to a fixed width first lines the badges up into a field of their own,
  which is what moving them here was meant to stop. `calcSessionLayout` widens
  the column by `harnessBadgeWidth` only when the badge is shown, so a
  single-agent machine renders byte-identical columns. `renderSessionRow` moves
  the badge back into `formatProject` when the terminal is too narrow for the
  origin column, because an untagged row on a mixed dashboard is an ambiguous row
  at any width.
- `calcSessionLayout` / `calcHistoryLayout` / `calcUsageLayout` in `layout.go`
  — every column width is a named constant there.
- Pad by `utf8.RuneCountInString`, never `len()`; the row gutter for the
  selection marker is carved out of the activity column, not added.

## `internal/web`

`server.go` binds `localhost:<port>`, synchronously, so a port conflict is
reported before the TUI takes over the screen. Two wrappers around the mux:

- `requireLocalHost` — 403 unless `Host` is `localhost`, `127.0.0.1` or
  `::1`. Binding to loopback does not stop DNS rebinding; this does. Keep it.
- `securityHeaders` — `nosniff`, `DENY`, a CSP, and `Cache-Control: no-cache`
  because `embed.FS` reports a zero mtime and browsers would otherwise cache
  stale JS across binary upgrades.

`ReadHeaderTimeout`, `ReadTimeout` and `IdleTimeout` are set;
**`WriteTimeout` is deliberately not**, because it covers the whole response
and would cut every SSE stream at the deadline.

Routes (`handlers.go`):

| Route | Returns |
|---|---|
| `/api/sessions` | active sessions + inactive ones from the last hour |
| `/api/history?days=N` | merged `DiscoverHistory` + stray inactive sessions, N ≤ 365 |
| `/api/sessions/timeline?file=&offset=&limit=&type=` | paged entries; `type` whitelisted, unknown values mean "all" |
| `/api/sessions/metrics?file=` | `ParseMetrics` |
| `/api/usage` | local token usage + API quota |
| `/api/quota` | API quota only (cheap poll for the header widget) |
| `/api/claude-status` | status page |
| `/api/events` | SSE |

`file` parameters go through `session.ValidateLogFilePath`, which requires the
resolved path to sit under the projects directory and end in `.jsonl`.

`sse.go`: the hub ticks every 2s but skips the scan when no client is
connected, sends a heartbeat every 30s, broadcasts `scan_error` instead of
going quiet under a green indicator, and turns a panic in the scanner into an
error on the `fatal` channel — the process shares a raw-mode terminal and only
`main`'s deferred cleanup can hand it back. `discoverSessions` is a package
var so tests can inject a scanner.

### Frontend (`web/static/`)

Vanilla JS in one IIFE, one CSS file, one HTML file, embedded with `go:embed`.
No framework, no bundler, no `package.json`. Iterate with
`make build && ./csm --web-only` and reload the browser.

- All HTML is built from template literals; every interpolated value goes
  through `esc()`.
- Live data arrives over `EventSource('/api/events')`; everything else is
  `fetch`. Pollers stop on `visibilitychange`.
- The header quota poll has a circuit breaker: the usage endpoint can stick at
  429 for the rest of a session, so a broken widget stays broken until reload
  rather than hammering it.
- Design tokens are the custom properties in `:root` at the top of
  `style.css`. Use them; don't introduce literal colours. The comment there
  records the contrast contract — text tokens are measured to WCAG AA 4.5:1
  against `--bg-hover`, the lightest surface they land on — and new tokens
  need the same measurement. Status colours (green / yellow / red) are
  semantic; everything else is neutral. Status glyphs are the same Unicode
  shapes the terminal uses (● ▲ ◉ ◌); don't introduce emoji.

## `internal/jump`

One function, `Focus(session.Session) (Result, error)`, split by build tag.
Anything it cannot do returns an error that unwraps to `ErrUnsupported` with a
user-facing sentence; the UI prints those verbatim, so they are written for the
user, not for a log.

### macOS (`jump_darwin.go`) — Ghostty tabs

Matching (`pick.go`, pure and tested on every platform): exact tty match wins
when the session is `PIDConfident`; otherwise match on working directory and
prefer the tab whose title does not look like a path, because Claude titles
its tab after the task and an idle shell titles itself after its cwd.
`Result.Matches` tells the UI when that was a guess. Once Ghostty exposes tty
everywhere, the guess disappears with no code change — the AppleScript already
reads `tty` inside a `try`.

`osascript` gets the script on stdin (nothing touches a shell) with a 30s
timeout: the first jump opens the macOS Automation consent dialog, and killing
`osascript` under it permanently records a denial.

### Linux (`jump_linux.go`) — compositor windows

Windows, not tabs: no Linux terminal exposes a scripting interface csm can rely
on, so a session in a background tab gets its window raised and no more.

`backend_linux.go` holds one `backend` per display server — Hyprland
(`hyprctl`), sway (`swaymsg`), X11 (`wmctrl`) — chosen by environment variable
rather than by which tools are installed, so a missing tool is reported as a
missing tool. GNOME and KDE on Wayland have no way for one client to focus
another's window; they get a sentence saying so. Every call is bounded at 3s:
these are local IPC round-trips, and unlike macOS there is no consent dialog to
wait on. `run` returns stdout *even on failure*, because hyprctl reports what
went wrong there rather than on stderr.

Hyprland is asked twice. 0.56 replaced dispatch arguments with a Lua API
(`hl.dsp.focus{window="address:0x…"}`), and the old `focuswindow address:0x…`
is a syntax error there while the new form is an unknown dispatcher on anything
older. Measured on 0.56.2, and the thing to get right if this ever needs
touching:

- Everything hyprctl says goes to **stdout**; stderr stays empty.
- A dispatch it could not parse exits **7** with `error: … ')' expected near …`
  or `hl.dispatch: expected a dispatcher`. Pre-0.56 it exits **0** and says
  `Invalid dispatcher`, so the exit status cannot carry this decision — only
  the body can. That is what `hyprRejectedForm` reads.
- A dispatch it understood exits 0 and says either `ok` or
  `warning: … window not found`. `"ok"` is the only success signal.

Only a rejected *spelling* falls through to the other form; a real verdict is
returned as it stands, so the legacy form's syntax complaint can no longer
overwrite the working form's answer and invent a version incompatibility.
`hyprWorkingForm` remembers which form answered, so the one that cannot work
here is spawned once per csm run rather than once per jump.

Matching (`pick_linux.go`, pure) is by process ownership, not title: a terminal
that forks per window is an ancestor of the Claude process inside it, so the
window whose pid is in `session.AncestorPIDs(GhostPID)` is the one. The chain
is walked **nearest-first and stops at the first pid that owns any window** —
collecting from every ancestor would pull in the terminal that terminal was
launched from, or an IDE's main window above a nested terminal, and report
unrelated windows as one single-instance terminal. csm's own window is dropped
first (`CSM: ` title prefix, written by `ui.buildTerminalTitle`): it shares the
pid under a single-instance terminal, is certainly not the session's, and
counting it made the guess below never fire.

Ownership stops being exact when one process owns many windows — Ghostty with
`--gtk-single-instance` (Omarchy's default), `foot --server`, `kitty
--single-instance`. Every window then reports the same pid, so ownership has
narrowed the field to one terminal's windows and cannot go further: Ghostty in
particular exports no per-window identifier into the child environment, so
there is nothing there to recover.

`windowByTitle` decides from there. The agent writes the session's title into
the window it runs in — omp renders `π ⠏ <title>`, Claude Code `✳ <title>` —
which makes it per-window evidence the shared pid is not, so a unique
containment hit (case-folded, titles under `minTitleHint` runes ignored as too
generic) returns `matches == 1` and is **not** reported as a guess. Several
hits mean the title separates nothing and the weaker rule gets its turn: where
exactly one candidate's title does not look like a path it is used and reported
as a guess; otherwise `Focus` declines and names the cause, because focusing
one of the user's windows at random is worse than not jumping. The advice to
disable single-instance mode is only offered to terminals that have one
(`singleInstanceTerminals`); GNOME Terminal and an IDE's integrated terminal
hit the same wall with no flag to turn off.

Unlike `--kill-ghosts`, `Focus` does not require `PIDConfident`. A second
`.jsonl` appearing after `/clear` or `/resume` drops confidence for half an
hour with one unambiguous process running, and refusing to jump for that long
is worse than jumping and saying so: the result carries `Guessed`, and
`Result.Message` then names the window title, which is the only way the user
can see a wrong pick.

## Writing tests

Sandbox the home directory and create the projects dir up front:

```go
home := t.TempDir()
t.Setenv("HOME", home)
os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755)
```

`Discover` reads the projects directory *before* scanning processes, so
without the `MkdirAll` a test passes or fails depending on whether the machine
happens to run Claude (`ghost_test.go` explains).

Reset the package caches between cases that go through `Discover()` —
`clearScanCaches()` (`ghost_test.go`) and `resetParseCache()`
(`cache_test.go`). Forgetting this is the usual cause of a test that passes
alone and fails in the suite.

Fixture helpers already exist; use them rather than hand-rolling JSONL:

| Helper | File | Builds |
|---|---|---|
| `writeLog`, `sampleLog` | `session/cache_test.go` | a log file, returning its mtime and size |
| `writeLogAt` | `session/findactivelogs_test.go` | a log with a chosen mtime |
| `writeLines`, `mustJSON` | `session/timeline_test.go` | a log from a slice of entries |
| `userLine` | `session/history_test.go` | a single user entry |
| `timelineFixture` | `web/handlers_test.go` | a log under a fake `$HOME` for the handlers |

Seams for the things you can't drive from a test: `listProcesses` (the process
scan), `getProcessCwdFn`, `processArgvFn`, `processTerminalFn`, `procRoot`
(procfs, Linux only), `browserCommand`, `originStoreDirFn`,
`parseLogFileWithLimit` / `parseOMPLogFileWithLimit` (trigger the oversized-line
path without writing 10 MB), `web.discoverSessions`, and the
`CSM_OMP_SESSIONS_DIR` env override (point omp discovery at a fixture tree).
Swap them and restore in `t.Cleanup`.

Write the failure message as the user-visible consequence, not the mismatch.
From `ghost_test.go`:

```go
t.Fatalf("PID %d is the working process and would be killed", pid)
```

That style is the strongest convention in the test suite, and it is what makes
a red test explain itself to whoever is reading it at 3am.
