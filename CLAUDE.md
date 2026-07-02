# Claude Code Project Guidelines

## Project Overview

Claude Sessions Monitor (csm) is a CLI tool that monitors Claude Code sessions across multiple projects. It reads JSONL log files from `~/.claude/projects/` and displays session status in a terminal dashboard.

## Tech Stack

- Go 1.21+
- No external dependencies (stdlib only)

## Project Structure

```
internal/
  session/  - Session discovery, log parsing, status detection, timeline/metrics
  ui/       - Terminal rendering (ANSI colors, formatting)
  watcher/  - File watching for live updates
  web/      - Web dashboard (HTTP server, REST API, SSE, embedded frontend)
    static/ - Frontend assets (HTML, CSS, JS) embedded via go:embed
main.go     - CLI entry point and flag handling
```

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

3. Push and create a pull request:
   ```bash
   git push -u origin feature/your-feature-name
   gh pr create
   ```

4. After review, merge the PR to `main`

## Release Workflow

### Automatic Releases (Recommended)

Releases are fully automated. When a PR is merged to `main`:

**Auto Tag and Release** (`.github/workflows/auto-tag.yaml`) does everything in one job:
   - Triggers on push to `main` branch
   - Gets the latest tag and increments the patch version (e.g., v0.3.8 → v0.3.9)
   - Creates and pushes the new tag
   - Builds binaries for darwin/linux × amd64/arm64 (plus `.deb`/`.rpm` packages)
   - Creates GitHub release with binaries attached
   - Hashes the built binaries, rewrites `Formula/csm.rb`, and commits it to
     `yepzdk/homebrew-tools` using `HOMEBREW_TAP_PAT`

Only **one** secret is involved: `HOMEBREW_TAP_PAT` in *this* repo, a token with
`contents: write` on `yepzdk/homebrew-tools`. The tap repo no longer runs its own
workflow, so there is no second copy of the token to keep in sync. When rotating
the PAT, update it here (`gh secret set HOMEBREW_TAP_PAT -R yepzdk/claude-sessions-monitor`).

### Manual Version Bumps

For major or minor version changes, manually push a tag:

```bash
git tag v1.0.0  # or v0.4.0 for minor bump
git push origin v1.0.0
```

This triggers `.github/workflows/release.yaml` which builds and releases.
The auto-tag workflow will continue from that version for subsequent patch releases.

### Troubleshooting releases

**Homebrew not seeing new version:**
1. Check if the release workflow succeeded: `gh run list` (look at the "Update Homebrew formula" step)
2. Check if the release was created: `gh release list`
3. Verify the formula was updated: `gh api repos/yepzdk/homebrew-tools/contents/Formula/csm.rb --jq '.content' | base64 -d | head -5`
4. If the formula step failed with an auth error, the `HOMEBREW_TAP_PAT` in this repo has likely expired — rotate it (see above).

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
