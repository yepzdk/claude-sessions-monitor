---
name: release
description: >
  Cut a csm release: roll CHANGELOG.md's [Unreleased] into a dated version
  section, PR it, then tag main so the release workflow builds binaries and
  updates the Homebrew formula. Use when the user says "release", "cut a
  release", "ship it", "tag a release", or invokes /release. Takes an optional
  version (e.g. /release 0.4.0, /release minor).
user-invocable: true
---

# Release Workflow (csm)

Pushing a `v*` tag is what releases csm. `.github/workflows/release.yaml` does
the rest: builds the binaries and packages, creates the GitHub release with
generated notes, and rewrites `Formula/csm.rb` in `yepzdk/homebrew-tools`.

**Never run `gh release create` here.** The workflow creates the release. Doing
it by hand races the workflow and produces a release with no artifacts.

There is no `package.json` and no version constant to edit — `make build`
derives the version from `git describe --tags`, so the tag *is* the version.
The only file this skill changes is `CHANGELOG.md`.

## Usage

- `/release` — infer the bump from what's in `[Unreleased]`, then confirm
- `/release 0.4.0` — use this exact version
- `/release minor` / `/release patch` / `/release major` — bump accordingly

## Phase 1: Preflight

Run together:
- `git status --short` — must be clean
- `git branch --show-current` — must be `main`
- `git fetch origin && git rev-list HEAD..origin/main --count` — must be 0

Not on `main`: switch and pull. Dirty tree: stop and tell the user. Behind: pull.

Then read `[Unreleased]` in `CHANGELOG.md`. **If it's empty, stop** — there is
nothing to release, and a version whose changelog says nothing is worse than no
release at all.

## Phase 2: Choose the version

`git describe --tags --abbrev=0` gives the current version.

If the user named a version or a bump type, use it. Otherwise propose one from
the *content* of `[Unreleased]`, not the commit count:

- Anything under `### Added` that users can act on, or a `### Removed` entry →
  **minor**
- Only `### Fixed` / internal `### Changed` → **patch**
- Breaking changes to the CLI or its flags → **major** (pre-1.0, still call it out)

State the reasoning in one line and confirm before continuing. Do not tag a
release the user hasn't agreed to.

## Phase 3: Roll the changelog

Rewrite `CHANGELOG.md` so that:

1. `## [Unreleased]` stays at the top, followed by a blank line and nothing else
2. A new `## [X.Y.Z] - YYYY-MM-DD` section (today's date) holds everything that
   was under `[Unreleased]`, keeping the `### Added` / `### Changed` / `###
   Fixed` / `### Removed` subsections and their order

Read entries as you go and fix anything that reads like a commit message rather
than a note to a user, but don't rewrite wholesale — the entries were written
when the change was fresh.

## Phase 4: PR the changelog

1. `git checkout -b chore/release-vX.Y.Z`
2. Commit `CHANGELOG.md` only:
   ```
   chore: release vX.Y.Z

   Co-authored-by: Claude <noreply@anthropic.com>
   ```
3. `git push -u origin chore/release-vX.Y.Z`
4. `gh pr create` — title `chore: release vX.Y.Z`, body = the new section's
   entries, so the PR shows exactly what ships
5. Report the PR URL, then **stop**. `main` is protected; the tag must point at
   a merged commit, so the user merges before anything is tagged.

## Phase 5: Tag (only after the user confirms the merge)

```bash
git checkout main && git pull
git tag vX.Y.Z
git push origin vX.Y.Z
```

Confirm `git log --oneline -1` is the release commit before tagging — a tag on
the wrong commit means re-cutting the release.

## Phase 6: Verify the release landed

The workflow takes ~1-2 minutes. Wait for it, then check all three outcomes —
the Homebrew step has broken silently before, so binaries existing is not proof
the release worked:

```bash
until [ "$(gh run list --workflow=release.yaml --limit 1 --json status --jq '.[0].status')" = "completed" ]; do sleep 10; done
gh run list --workflow=release.yaml --limit 1

# 8 artifacts: darwin/linux × amd64/arm64, plus .deb ×2 and .rpm ×2
gh release view vX.Y.Z --json assets --jq '.assets[].name'

# Formula version must match the tag
gh api repos/yepzdk/homebrew-tools/contents/Formula/csm.rb --jq '.content' | base64 -d | grep '^  version'

```

Report the release URL, the artifact count, and the formula version. If the
formula is stale, the run's "Update Homebrew formula" step is where to look —
usually an expired `HOMEBREW_TAP_PAT` (see `CLAUDE.md`).
