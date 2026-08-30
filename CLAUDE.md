# Project Guidelines

## Project Overview

Coding Sessions Monitor (csm) is a CLI tool that monitors coding agent sessions across multiple projects. It reads JSONL logs from Claude Code (`~/.claude/projects/`) and Oh My Pi (`~/.omp/agent/sessions/`) and shows both in one terminal or web dashboard. The repository keeps its original `claude-sessions-monitor` name so existing clones and `go install` paths keep working.

## Tech Stack

- Go 1.25+
- Standard library, plus `golang.org/x/term` for raw terminal input and `golang.org/x/sys` for the macOS process table. No other third-party dependencies.

Tests sandbox `$HOME`. To run them against a clean one, keep the caches where they are:
`HOME=<tmpdir> GOMODCACHE=$(go env GOMODCACHE) GOCACHE=$(go env GOCACHE) go test ./... -count=1`

## Project Structure

```
internal/
  session/  - Session discovery for both agents, log parsing, status detection, timeline/metrics
  ui/       - Terminal rendering (ANSI colors, formatting)
  jump/     - Bring a session's terminal to the front: the tab on macOS (Ghostty), the window on Linux (Hyprland, sway, wmctrl)
  web/      - Web dashboard (HTTP server, REST API, SSE, embedded frontend)
    static/ - Frontend assets (HTML, CSS, JS) embedded via go:embed
main.go     - CLI entry point and flag handling
docs/ARCHITECTURE.md - Contributor map: data flow, status rules, ghosts, caches, test helpers
```

Read `docs/ARCHITECTURE.md` before changing `internal/session`, and `.github/CONTRIBUTING.md` for the style, test and changelog rules.

## Development Workflow

### Branch Protection

The `main` branch is protected:
- Direct pushes are not allowed
- All changes must go through pull requests
- PRs must be reviewed before merging

### Making Changes

1. Create a feature branch from `main`:
   ```bash
   git checkout main
   git pull origin main
   git checkout -b feature/your-feature-name
   ```

2. Make your changes and commit:
   ```bash
   git add .
   git commit -m "Description of changes"
   ```
   Run `make check` first: gofmt, `go vet`, golangci-lint once per `GOOS`, build and tests. That is exactly what CI runs.

3. Push and create a pull request:
   ```bash
   git push -u origin feature/your-feature-name
   gh pr create
   ```

4. After review, merge the PR to `main`

## Release Workflow

### Cutting a release

`/csm-release` drives this end to end (`.claude/skills/csm-release/SKILL.md`): it rolls
the changelog, opens the PR, tags `main` after you merge, and verifies the
release, artifacts and Homebrew formula all landed. The manual steps below are
what it does, and what to fall back on.

Releases are triggered by pushing a tag — merging to `main` does not release
anything. This is deliberate: merges are cheap and frequent, releases are a
decision, and letting every merge cut a patch produced runs of versions minutes
apart that said nothing about what changed.

```bash
git checkout main && git pull
git tag v0.4.0        # pick the bump: patch, minor, or major
git push origin v0.4.0
```

That triggers `.github/workflows/release.yaml`, which does everything in one job:
   - Builds binaries for darwin/linux × amd64/arm64 (plus `.deb`/`.rpm` packages)
   - Creates the GitHub release with binaries attached and generated notes
   - Hashes the built binaries, rewrites `Formula/csm.rb`, and commits it to
     `yepzdk/homebrew-tools` using `HOMEBREW_TAP_PAT`

Pick the version from what actually changed since the last tag — `CHANGELOG.md`'s
`[Unreleased]` section is the place to look. Nothing computes it for you.

It also publishes the AUR package `csm-bin` — see below — in a second job.

Two secrets are involved. The first is `HOMEBREW_TAP_PAT` in *this* repo, a token with
`contents: write` on `yepzdk/homebrew-tools`. The tap repo does not run its own
workflow, so there is no second copy of the token to keep in sync. When rotating
the PAT, update it here (`gh secret set HOMEBREW_TAP_PAT -R yepzdk/claude-sessions-monitor`).

### AUR (`csm-bin`)

The `aur` job renders `packaging/aur/PKGBUILD.template` for the tag, generates
`.SRCINFO` with `makepkg`, and pushes both to `ssh://aur@aur.archlinux.org/csm-bin.git`.
Hashes come from the release's own `checksums.txt`, so the AUR package, the
Homebrew formula, `install.sh` and `csm -upgrade` all vouch for the same bytes.

The job is **dormant until `AUR_SSH_KEY` is set** — without it the step logs
that and exits 0, so releases stay green on a repo that isn't wired to the AUR.

> **Blocked as of 2026-08-29:** aur.archlinux.org has account registration
> temporarily closed, so step 1 below cannot be completed yet. Everything else
> — the PKGBUILD, the renderer, the release job — is finished and tested;
> `csm-bin` goes live the moment an account can be created. Re-check
> https://aur.archlinux.org/register periodically.

To enable it:

1. Create an account at https://aur.archlinux.org and add an SSH public key to it.
2. Generate a dedicated keypair for CI (`ssh-keygen -t ed25519 -f aur -C csm-ci`),
   register the public half with that account, and store the private half here:
   `gh secret set AUR_SSH_KEY -R yepzdk/claude-sessions-monitor < aur`
3. Create the package once, by hand — the AUR has no "create repo" API, the
   first push does it:
   ```bash
   git clone ssh://aur@aur.archlinux.org/csm-bin.git   # empty, this is expected
   cd csm-bin
   ../packaging/aur/render.sh 0.7.0 checksums.txt ../LICENSE > PKGBUILD
   makepkg --printsrcinfo > .SRCINFO
   git add PKGBUILD .SRCINFO && git commit -m "Initial import" && git push
   ```

To test a PKGBUILD change without releasing:

```bash
packaging/aur/render.sh <version> <checksums.txt> LICENSE > /tmp/aur/PKGBUILD
cd /tmp/aur && makepkg --printsrcinfo > .SRCINFO && makepkg -f
```

### Troubleshooting releases

**Homebrew not seeing new version:**
1. Check if the release workflow succeeded: `gh run list` (look at the "Update Homebrew formula" step)
2. Check if the release was created: `gh release list`
3. Verify the formula was updated: `gh api repos/yepzdk/homebrew-tools/contents/Formula/csm.rb --jq '.content' | base64 -d | head -5`
4. If the formula step failed with an auth error, the `HOMEBREW_TAP_PAT` in this repo has likely expired — rotate it (see above).

**AUR package not updating:** check the `aur` job in `gh run list`. "AUR_SSH_KEY
is not set" means the secret is missing (see above); an SSH failure means the key
was rotated or removed from the AUR account.

**Users hitting `Refusing to load formula ... untrusted tap`:** this is Homebrew's
third-party-tap policy, not a release problem. Run `brew trust yepzdk/tools` once.

**Manual formula update (if automation completely fails):**
1. Get SHA256 hashes for binaries:
   ```bash
   for arch in darwin-arm64 darwin-amd64 linux-arm64 linux-amd64; do
     echo "$arch: $(curl -sL "https://github.com/yepzdk/claude-sessions-monitor/releases/download/vX.X.X/csm-$arch" | shasum -a 256 | cut -d' ' -f1)"
   done
   ```
2. Update `Formula/csm.rb` in `yepzdk/homebrew-tools` with new version and hashes

### Related repositories

- Main repo: `yepzdk/claude-sessions-monitor`
- Homebrew tap: `yepzdk/homebrew-tools` (contains `Formula/csm.rb`)
