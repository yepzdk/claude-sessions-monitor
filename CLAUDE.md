# Claude Code Project Guidelines

## Project Overview

Claude Sessions Monitor (csm) is a CLI tool that monitors Claude Code sessions across multiple projects. It reads JSONL log files from `~/.claude/projects/` and displays session status in a terminal dashboard.

## Tech Stack

- Go 1.21+
- No external dependencies (stdlib only)

## Project Structure

```
internal/
  session/  - Session discovery, log parsing, status detection
  ui/       - Terminal rendering (ANSI colors, formatting)
  watcher/  - File watching for live updates
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

1. **Auto-tagging** (`.github/workflows/auto-tag.yaml`):
   - Triggers on push to `main` branch
   - Gets the latest tag and increments the patch version (e.g., v0.3.8 → v0.3.9)
   - Creates and pushes the new tag

2. **Release build** (`.github/workflows/release.yaml`):
   - Triggers on the new tag push
   - Builds binaries for darwin/linux × amd64/arm64
   - Creates GitHub release with binaries attached
   - Sends `repository_dispatch` event to `yepzdk/homebrew-tools`

3. **Homebrew tap update** (`yepzdk/homebrew-tools`):
   - Workflow triggers on `repository_dispatch` with type `update-csm`
   - Downloads the new binaries and calculates SHA256 hashes
   - Updates `Formula/csm.rb` with new version and hashes
   - Commits and pushes automatically

### Manual Version Bumps

For major or minor version changes, manually create a tag before merging:

```bash
git tag v1.0.0  # or v0.4.0 for minor bump
git push origin v1.0.0
```

The auto-tag workflow will then continue from that version for subsequent patch releases.

### Troubleshooting releases

**Homebrew not seeing new version:**
1. Check if release workflow succeeded: `gh run list`
2. Check if release was created: `gh release list`
3. Check homebrew-tools workflow: `gh run list -R yepzdk/homebrew-tools`
4. Verify formula was updated: `gh api repos/yepzdk/homebrew-tools/contents/Formula/csm.rb --jq '.content' | base64 -d | head -5`

**Manual trigger (if dispatch failed):**
```bash
gh workflow run update-csm.yml -R yepzdk/homebrew-tools -f version=X.X.X
```

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
