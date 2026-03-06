# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- Make usage/quota fetching fully on-demand instead of periodic polling
- Terminal: usage data fetched only on view entry (`u`) or manual refresh (`r`)
- Web: usage data fetched via REST on tab switch or refresh button click, no longer broadcast via SSE
- Increase API quota cache TTL from 30s to 60s to reduce Anthropic API request frequency

### Fixed

- Include output tokens in context window calculation to match Claude Code's reported usage
