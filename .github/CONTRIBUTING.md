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
2. Make your change, and run `make build` and `go test ./...`
3. Add an entry to the `[Unreleased]` section of `CHANGELOG.md`
4. Open a pull request

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/):
`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, `perf:`, `ci:`,
`build:`, `style:`.

Example: `fix: resolve stuck Working status on idle sessions`

## Releases

Releases are automated — merging to `main` tags a new patch version and
publishes binaries. You do not need to bump any version yourself.
