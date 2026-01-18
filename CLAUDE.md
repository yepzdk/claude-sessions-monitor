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

## Release Workflow

### How to release a new version

1. Commit your changes to `main`
2. Create and push a version tag:
   ```bash
   git tag v0.x.x
   git push origin v0.x.x
   ```

### What happens automatically

1. **GitHub Actions** (`.github/workflows/release.yaml`):
   - Triggers on tag push matching `v*`
   - Builds binaries for darwin/linux × amd64/arm64
   - Creates GitHub release with binaries attached
   - Sends `repository_dispatch` event to `yepzdk/homebrew-tools`

2. **Homebrew tap update** (`yepzdk/homebrew-tools`):
   - Workflow triggers on `repository_dispatch` with type `update-csm`
   - Downloads the new binaries and calculates SHA256 hashes
   - Updates `Formula/csm.rb` with new version and hashes
   - Commits and pushes automatically

**Note:** Both workflows need to be in place for full automation:
- This repo: release.yaml must include the `repository_dispatch` step
- homebrew-tools: must have a workflow listening for `update-csm` events

### Troubleshooting releases

**Homebrew not seeing new version:**
1. Check if release workflow succeeded: `gh run list`
2. Check if release was created: `gh release list`
3. Check homebrew-tools workflow: `gh run list -R yepzdk/homebrew-tools`
4. Verify formula was updated: `gh api repos/yepzdk/homebrew-tools/contents/Formula/csm.rb --jq '.content' | base64 -d | head -5`

**Manual formula update (if automation fails):**
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
