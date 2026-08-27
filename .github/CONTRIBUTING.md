# Contributing

Thanks for taking the time to contribute.

## Getting started

```bash
git clone https://github.com/yepzdk/claude-sessions-monitor.git
cd claude-sessions-monitor
make build
./csm
```

Go 1.25 or newer is required (`go.mod` pins 1.25.6). The project uses the
standard library plus `golang.org/x/term` for raw terminal input — please
don't add further dependencies.

Read [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) before changing
`internal/session`. It covers the data flow, how a session's status is
inferred, why ghost detection is as cautious as it is, and the test helpers
you should reuse.

## Workflow

`main` is protected: all changes go through a pull request.

1. Branch off `main` — `feature/short-description`
2. Make your change and run `make check` — gofmt, `go vet`, `make lint`, the
   build and the tests, which is exactly what CI runs
3. Add an entry to the `[Unreleased]` section of `CHANGELOG.md`
4. Open a pull request

CI runs `make check` on both Linux and macOS for every pull request.

`make lint` runs [golangci-lint](https://golangci-lint.run) against
`.golangci.yml` twice, for `GOOS=linux` and for `GOOS=darwin`: the jump
feature and origin detection are split by build tag, and a single pass leaves
the other platform's files unchecked. The pinned version is built with Go
1.26, so the first run installs that toolchain; `GOTOOLCHAIN=local` blocks
the install.

The config carries no baseline, so `main` is expected to report zero findings.
A finding that is correct as written gets a `//nolint:<linter>` with the reason
at the site, not an entry in an ignore list.

## Code style

`gofmt` is the only style authority for Go code — it is not configurable and
not a matter of preference, so please don't hand-tune formatting it would
change. Run `make fmt` before committing; CI fails on anything it would
reformat.

This keeps alignment churn (struct tags, map literals) out of feature diffs,
where it otherwise buries the real change. If a diff shows formatting you
didn't intend, it usually means a file was committed unformatted earlier —
fix that in its own commit rather than folding it into a feature change.

The codebase explains itself in comments: nearly every non-obvious decision
has a sentence next to it saying why. Keep that up — when you make a choice a
reader could reasonably question, leave the reason at the site.

An `.editorconfig` covers the non-Go files (JS, CSS, YAML, Markdown); most
editors pick it up automatically.

## Frontend

The web dashboard in `internal/web/static/` is plain HTML, CSS and
JavaScript, embedded into the binary with `go:embed`. There is no framework,
bundler or `package.json`, and that is deliberate — please don't introduce
one. Iterate with `make build && ./csm --web-only` and reload the browser.

Use the design tokens defined in `:root` at the top of `style.css` rather than
literal colours, and keep the contrast contract documented there. Escape
every value you interpolate into HTML with `esc()`. The architecture doc has
the rest.

## Writing tests

Tests sandbox `$HOME` with `t.Setenv` and build fixture logs with the helpers
listed in [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md#writing-tests). Two
things bite everyone once: the projects directory has to exist under the fake
home, and the package-level caches have to be reset between cases. Write
failure messages that say what the user would have seen go wrong, not just
which values differed.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, `perf:`, `ci:`,
`build:`, `style:`.

Example: `fix: resolve stuck Working status on idle sessions`

## Releases

Releases are triggered by pushing a tag, not by merging. Merging to `main`
publishes nothing; a maintainer picks the version from what changed and pushes
`vX.Y.Z`, which builds the binaries and updates the Homebrew formula. You do
not need to bump any version yourself.
