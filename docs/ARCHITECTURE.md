# Architecture

A map for contributors. It tells you where things live, what the non-obvious
rules are and why, and which helpers to reuse. It links to files, not line
numbers; the code carries a rationale comment at nearly every non-obvious
decision, and that comment is the authority if the two ever disagree.

## Data flow

```
~/.claude/projects/<encoded-cwd>/<session>.jsonl      the OS process table
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
2. `getRunningClaudeDirs()` reads the process table from the kernel
   (`listProcesses`, see [Platform code](#platform-code)), keeps processes whose
   name ends in `claude`, resolves each pid's cwd and keys the result by
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

A failed process scan aborts `Discover` with an error rather than returning an
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
  unconfident pairing would be a coin flip over which session dies. `jump`
  applies the same rule before trusting a pid's tty.
- A ghost that fails that recheck for any reason but having exited is reported
  as a failure rather than dropped, so "csm listed it and refused to signal it"
  cannot read as "it had already exited".

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
| `processScanCache` | 2s | one read of the process table per tick, not per caller |
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

Subprocesses, all macOS-only: `lsof` (a process's cwd), `ps` (origin
detection, and the tty lookup in jump), `security` (Keychain) and `osascript`
(jump). The native calls for the first and last are libproc and
Security.framework, both cgo, and the release workflow cross-builds the darwin
targets from a Linux runner in one job. On Linux csm spawns nothing except
`xdg-open`.

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
- `jump/jump_darwin.go` / `jump_other.go`: focus a terminal tab, or report
  that we can't.

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

Seams for the things you can't drive from a test: `listProcesses` (the process
scan), `getProcessCwdFn`, `procRoot` (procfs, Linux only), `browserCommand`,
`originStoreDirFn`, `parseLogFileWithLimit` (trigger the oversized-line
path without writing 10 MB), `web.discoverSessions`. Swap them and restore in
`t.Cleanup`.

Write the failure message as the user-visible consequence, not the mismatch.
From `ghost_test.go`:

```go
t.Fatalf("PID %d is the working process and would be killed", pid)
```

That style is the strongest convention in the test suite, and it is what makes
a red test explain itself to whoever is reading it at 3am.
