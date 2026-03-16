# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- CSMMenuBar `.app` bundles attached to GitHub Releases (arm64 + amd64)
- Homebrew cask: `brew install --cask yepzdk/tools/csm-menubar`
- macOS menu bar app can be packaged as a `.app` bundle (`make menubar-app`) for Spotlight/Launchpad/Applications
- macOS menu bar app (SwiftUI, macOS 13+) for persistent session visibility without a terminal or browser
  - Dynamic status icon with color reflecting aggregate session state
  - Session popover with project, status, branch, context bar, and last activity
  - Smart `csm --web-only` process management (detects existing server, starts if needed, cleans up on quit)
  - "Open Web Dashboard" link for history and detailed views
- `--web-only` flag for headless web server mode (no terminal UI required)

### Changed

- macOS menu bar app now bundles the `csm` binary — no separate installation required
- Make usage/quota fetching fully on-demand instead of periodic polling
- Terminal: usage data fetched only on view entry (`u`) or manual refresh (`r`)
- Web: usage data fetched via REST on tab switch or refresh button click, no longer broadcast via SSE
- Increase API quota cache TTL from 30s to 60s to reduce Anthropic API request frequency

### Fixed

- Include output tokens in context window calculation to match Claude Code's reported usage
