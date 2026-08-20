# Contributing

Thanks for taking the time to contribute.

## Getting started

```bash
git clone https://github.com/yepzdk/claude-sessions-monitor.git
cd claude-sessions-monitor
make build
```

Go 1.21+ is required. The project uses the standard library only — please
avoid adding external dependencies.

## Workflow

`main` is protected: all changes go through a pull request.

1. Branch off `main` — `feature/short-description`
2. Make your change, and run `gofmt -w .`, `make build`, `go vet ./...` and
   `go test ./...`
3. Add an entry to the `[Unreleased]` section of `CHANGELOG.md`
4. Open a pull request

CI runs all four checks on every pull request.

## Code style

`gofmt` is the only style authority for Go code — it is not configurable and
not a matter of preference, so please don't hand-tune formatting it would
change. Run `gofmt -w .` before committing; CI fails on anything it would
reformat.

This keeps alignment churn (struct tags, map literals) out of feature diffs,
where it otherwise buries the real change. If a diff shows formatting you
didn't intend, it usually means a file was committed unformatted earlier —
fix that in its own commit rather than folding it into a feature change.

An `.editorconfig` covers the non-Go files (JS, CSS, YAML, Markdown); most
editors pick it up automatically.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, `perf:`, `ci:`,
`build:`, `style:`.

Example: `fix: resolve stuck Working status on idle sessions`

## Releases

Releases are automated — merging to `main` tags a new patch version and
publishes binaries. You do not need to bump any version yourself.
