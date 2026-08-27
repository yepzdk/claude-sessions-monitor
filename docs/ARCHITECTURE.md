# Architecture

A map for contributors. It tells you where things live, what the non-obvious
rules are and why, and which helpers to reuse. It links to files, not line
numbers; the code carries a rationale comment at nearly every non-obvious
decision, and that comment is the authority if the two ever disagree.

## Data flow

```
~/.claude/projects/<encoded-cwd>/<session>.jsonl     ps ax -o pid=,ppid=,comm=
                    │                                          │
                    └──────────── session.Discover() ──────────┘
                                        │
                                   []session.Session
                    ┌───────────────────┼───────────────────┐
                 ui (TUI)        web (JSON + SSE)      jump (focus tab)
```

`internal/session` is the only package that reads logs or looks at processes.
`ui`, `web` and `jump` consume `[]Session` and never go back to the sources.
`session.DiscoverHistory(days)` is the parallel path for past sessions, and
`session.ParseTimeline` / `ParseMetrics` read a single log on demand for the
web detail panel.

`main.go` dispatches on flags in this order: `-v`, `-kill-ghosts`, `-history`,
`-l` (with `-json`), `-web-only`, then the default live view. The live loop
owns the `ViewMode` (live / history / usage); `ui` only exposes the three
renderers. The ticker runs at `-interval` (2s); the usage view never
auto-refreshes and the history view is throttled to once per 30s.

## `internal/session`

### Discovery (`session.go`)

1. `ClaudeProjectsDir()` is `$HOME/.claude/projects`. Every subdirectory is a
   project; its name is Claude Code's encoding of the working directory
   (`encodeProjectPath`: every rune outside `[A-Za-z0-9-]` becomes `-`).
2. `getRunningClaudeDirs()` runs `ps ax -o pid=,ppid=,comm=` (no shell), keeps
   rows whose command ends in `claude`, resolves each pid's cwd
   (`/proc/<pid>/cwd` on Linux, `lsof -p` on macOS) and keys the result by
   `encodeProjectPath(cwd)`. Because both sides use the same encoding, joining
   processes to project directories is a plain string-key match.
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

A failed `ps` scan aborts `Discover` with an error rather than returning an
empty process map: an empty map is indistinguishable from "nothing running"
and would mark every session Inactive.

### Status inference (`determineStatus` in `session.go`)

Reads four kinds of log entries: `user`, `assistant`, `system` with subtype
`turn_duration`, and the progress heartbeats (`progress`, `hook_progress`,
`agent_progress`). Three thresholds: 2 minutes for "recent activity", 30
seconds for the log file's mtime, 5 minutes for "the whole session is stale".

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

### Processes, ghosts and the pairing pitfall

Claude Code exposes no pid↔session mapping. Logs are sorted newest-first; pids
arrive in `ps` order. The two orderings are unrelated, so pairing log *i* with
pid *i* is a positional guess. Everything downstream is built around that:

- `isRunning = len(pids) > 0` — any claude process in the project directory
  marks every candidate log as running. Deliberately generous: a wrong
  "running" self-corrects to Waiting through content staleness, whereas a
  wrong "inactive" hides a live session from the dashboard entirely.
- `Session.PIDConfident` is true only when the directory has exactly one log
  and exactly one process. It is `json:"-"`; the web layer never sees it.
- `IsGhost = running && parent pid is 1 && LastActivity older than GhostThreshold (1h)`,
  computed in `applyParsedLog` because it needs `LastActivity`. Orphaning is
  the signal; the hour of silence protects the one legitimate orphan, a
  headless `claude -p` whose shell has exited. Known ceiling: a Linux
  subreaper adopts orphans instead of pid 1 and this misses them.
- `ghostsFrom` refuses any session that is not `PIDConfident`, and
  `KillGhostProcesses` re-checks the pid is still a claude process before
  `SIGTERM`. An unconfident pairing would be a coin flip over which session
  dies. `jump` applies the same rule before trusting a pid's tty.

### `Degraded`

`Session.Degraded` (and `HistorySession.Degraded`) is non-empty when a log
could not be read to the end — typically a line over the 10 MB scanner cap or
an I/O error. Partial data is kept unless zero entries parsed
(`isFatalParseError` in `cache.go`); the session still gets a status and stays
visible. Every view must render the marker so the numbers are not read as
measurements: `[?]` in `ui/ui.go` and `ui/history.go`, the `?` badge in
`web/static/app.js`. A new view or column that shows counts needs the same.

### Caches (`cache.go`, `quota.go`, `status.go`)

| Cache | Key / TTL | Why |
|---|---|---|
| `parseCache` | log path; valid while `(mtime, size)` unchanged | skip re-parsing multi-MB logs every tick |
| `processScanCache` | 2s | one `ps`/`lsof` round per tick, not per caller |
| `resultCache` | 1s | TUI loop, SSE hub and HTTP handlers collapse to one scan |
| `apiQuotaCache` | 60s | the quota endpoint rate-limits hard |
| `claudeStatusCache` | 60s | status page, fetched on demand only |

All are package state. Tests that go through `Discover()` must reset them —
see [Writing tests](#writing-tests).

### What csm touches outside its own process

Reads: `~/.claude/projects/` (logs, `sessions-index.json`), and the OAuth token
from the macOS Keychain item `Claude Code-credentials` or
`~/.claude/.credentials.json` on Linux (`oauth.go`).

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
scan), `originStoreDirFn`, `parseLogFileWithLimit` (trigger the oversized-line
path without writing 10 MB), `web.discoverSessions`. Swap them and restore in
`t.Cleanup`.

Write the failure message as the user-visible consequence, not the mismatch.
From `ghost_test.go`:

```go
t.Fatalf("PID %d is the working process and would be killed", pid)
```

That style is the strongest convention in the test suite, and it is what makes
a red test explain itself to whoever is reading it at 3am.
