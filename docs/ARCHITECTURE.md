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
ps ax -o pid=,ppid=,tty=,args=  ──►  classifyProcess()  ──►  both above  ──┤
                                                    []session.Session  ◄──┘
                                                             │
                                    ┌────────────────────────┼────────────────────┐
                                 ui (TUI)            web (JSON + SSE)     jump (focus tab)
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

`getRunningHarnessProcs()` runs `ps ax -o pid=,ppid=,tty=,args=` (no shell) and
returns one `harnessProcess` per recognised agent, with its harness, cwd
(`/proc/<pid>/cwd` on Linux, `lsof -p` on macOS), controlling terminal and
whether it has been orphaned.

`args=`, not `comm=`, because an agent is not always its own `argv[0]`: omp runs
as `bun /path/to/omp`, so `comm` is `bun`. `classifyProcess` therefore matches
**argv token basenames, never command-line substrings**. This is not fussiness:
a machine with omp installed runs a puppeteer Chrome under `~/.omp/puppeteer/`
and a Python REPL out of `/var/folders/.../T/omp-python-runner/`, and
`strings.Contains(argv, "omp")` claims both. `--kill-ghosts` would then SIGTERM
a browser. Don't loosen it.

A failed `ps` scan aborts `Discover` with an error rather than returning an
empty slice: empty is indistinguishable from "nothing running" and would mark
every session Inactive.

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

`OMPSessionsDir()` is `$HOME/.omp/agent/sessions`, overridable with
`CSM_OMP_SESSIONS_DIR` — omp relocates its store for `--profile` and
`--session-dir`, so unlike Claude Code the default path is not a guarantee.

omp's bucket encoding is home-relative, has had several spellings, and is
migrated best-effort on access, so csm does not reimplement it. Instead
`ompHeaderCWD` reads the working directory out of the newest log's header (two
lines, one file per bucket) and joins that to processes keyed by raw cwd.
`findActiveLogs` is reused unchanged; it already skips directories, which is what
excludes omp's per-session artifact directory sitting beside each log.

What the format demands of the parser:

- The physical first line is a fixed-width 256-byte `{"type":"title"}` slot, not
  the header. A parser that assumed otherwise reads no `cwd` and every omp
  session loses its project name and its process.
- Entries form an append-only **tree** (`id`/`parentId` + leaf pointer). csm
  reads the physical tail. Known ceiling: after a rewind or branch switch the
  tail may not be on the live branch. It is what the file's mtime reflects, and
  walking from the leaf would mean reading whole multi-MB files every tick.
- `message.content` is either a block array or a bare string, so it stays
  `json.RawMessage` and `ompMessage.text()` handles both. Same for `custom.data`,
  whose shape depends on `customType`.
- An unparseable line costs one entry, not the session. omp's own loader is
  lenient here too.

Pairing has no registry to work from — omp writes no pid anywhere. It does write
a best-effort breadcrumb per terminal (`terminal-sessions/<terminal-id>` →
cwd + log path), and the terminal id is the tty when the process has one, so
`pairOMPProcess` joins breadcrumbs to the scan's `tty` column for an exact
pairing. It degrades to running-with-no-pid (`PIDConfident` false) when omp used
an env-derived id (`TMUX_PANE`, `CMUX_SURFACE_ID`, ...) that `ps` cannot see, or
the process has no terminal. Two breadcrumbs naming one log drop the pairing
rather than guess, the same rule `pairProcess` applies to a duplicated session id.

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

Claude Code (2.1.x) keeps a registry at `~/.claude/sessions/<pid>.json` with
the pid, session id and cwd of every live session; `registry.go` reads it.
Every entry is validated against the set of `claude` pids `ps` found —
never against the bare pid — because a crash or reboot leaves files behind
and the pid gets reused. `pairProcess` then decides, per log: registry hit →
that session's own pid, confident; registry present and every pid in the
directory accounted for → not running; a pid the registry cannot name →
running, no pid. Two files carrying one session id (`--resume` in two tabs)
drop the id rather than guess. The registry snapshot is taken under the same
lock and TTL as the `ps` scan so the two views cannot disagree mid-tick.

Without a registry (older Claude Code) the pairing is positional: logs are
sorted newest-first, pids arrive in `ps` order, the two are unrelated, and
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
  `KillGhostProcesses` re-checks the pid with `isHarnessProcess(pid, ghost.Harness)`
  before `SIGTERM` — the *session's* harness, not "any known agent", because pids
  are unique per machine and not per agent. An unconfident pairing, or an
  unattributed one, would be a coin flip over which session dies. `jump` applies
  the same rule before trusting a pid's tty.

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
| `processScanCache` | 2s | one `ps`/`lsof` round per tick, not per caller. Guarded by `processScanValid`, not a nil check: no agent running is a legitimate result |
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

Subprocesses: `ps` (all platforms), `lsof` and `security` (macOS),
`osascript` (macOS, jump only).

### Platform code

Build-tagged pairs, one file per OS with identical function signatures:

- `session/origin_detect_darwin.go` / `origin_detect_linux.go` — read a
  process's environment and parent chain (`ps -E` vs `/proc`).
- `jump/jump_darwin.go` / `jump_other.go` — focus a terminal tab, or report
  that we can't.

Runtime `runtime.GOOS` switches, not build tags: `getProcessCwd`,
`GetOAuthToken`, `openBrowser` in `main.go`.

There is no `origin_detect_*.go` for other systems, so `internal/session`
does not build on Windows or BSD. That is known; don't fix it in passing.

When you touch any of these, compile both halves:

```bash
GOOS=darwin go vet ./... && GOOS=linux go vet ./...
```

CI runs the full check on both `ubuntu-latest` and `macos-latest`, and
`make lint` runs golangci-lint once per `GOOS` for the same reason.
`jump/livecheck_manual_test.go` is behind the `manual` build tag because it
needs a real Ghostty, live sessions and macOS Automation consent; the file
header says when and how to run it.

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
- `MixedHarnesses` / `FilterByHarness` — the agent label renders only in mixed
  company, decided *before* the filter is applied so narrowing to one agent
  still says which one is on screen and the rows don't re-flow when `f` is
  pressed. `f` is a display filter; both agents are always scanned.
- The agent badge lives in the origin cell (`formatOrigin`), *after* the origin
  name: which agent and what launched it are one fact about where a session came
  from, and the origin is the column's subject. `calcSessionLayout` widens the
  column by `harnessBadgeWidth` only when the badge is shown, so a single-agent
  machine gets byte-identical columns, and the name is padded to the remainder so
  badges align rather than trailing each name at a different offset.
  `renderSessionRow` moves the badge back into `formatProject` when the terminal
  is too narrow for the origin column, because an untagged row on a mixed
  dashboard is an ambiguous row at any width.
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
macOS + Ghostty only for now; every other case returns an error that unwraps
to `ErrUnsupported` with a user-facing sentence.

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

Seams for the things you can't drive from a test: `listProcesses` (the `ps`
scan), `processArgs` (the pre-SIGTERM pid recheck), `originStoreDirFn`,
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
