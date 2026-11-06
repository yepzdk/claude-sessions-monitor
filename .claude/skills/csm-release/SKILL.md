---
name: csm-release
description: >
  Cut a csm release: write CHANGELOG.md's dated version section from the pull
  requests merged since the last tag, PR it, then tag main so the release
  workflow builds binaries, publishes that section as the release notes and
  updates the Homebrew formula. Use for ANY release of this repo — when the
  user says "release", "cut a release", "ship it", "tag a release", or invokes
  /csm-release or /release. Takes an optional version (e.g. /csm-release 0.4.0,
  /csm-release minor). Prefer this over the generic `release` skill here: this
  repo has no package.json, and its release is triggered by pushing a tag, not
  by calling `gh release create`.
user-invocable: true
---

# Release Workflow (csm)

Pushing a `v*` tag is what releases csm. `.github/workflows/release.yaml` does
the rest: builds the binaries and packages, creates the GitHub release with the
tag's `## [X.Y.Z]` section of `CHANGELOG.md` as its notes, and rewrites
`Formula/csm.rb` in `yepzdk/homebrew-tools`. A tag with no matching section
fails the workflow before anything is published.

**Never run `gh release create` here.** The workflow creates the release. Doing
it by hand races the workflow and produces a release with no artifacts.

There is no `package.json` and no version constant to edit — `make build`
derives the version from `git describe --tags`, so the tag *is* the version.
The only file this skill changes is `CHANGELOG.md`.

Feature and fix pull requests never touch `CHANGELOG.md`. Their titles and
descriptions say what changed and why, and the release PR is the one place
those become changelog prose. That keeps parallel PRs from conflicting on a
shared `[Unreleased]` section.

## Usage

- `/csm-release` — infer the bump from the PRs merged since the last tag, then
  confirm
- `/csm-release 0.4.0` — use this exact version
- `/csm-release minor` / `patch` / `major` — bump accordingly

Named `csm-release` rather than `release` because a global `release` skill
exists and wins the name. That one bumps `package.json` and finishes with
`gh release create`, both of which are wrong here — see the note above about
racing the workflow.

## Phase 1: Preflight

Run together:
- `git status --short` — must be clean
- `git branch --show-current` — must be `main`
- `git fetch origin --tags && git rev-list HEAD..origin/main --count` — must be 0

Not on `main`: switch and pull. Dirty tree: stop and tell the user. Behind: pull.

## Phase 2: Collect what ships

```bash
LAST_TAG=$(git describe --tags --abbrev=0)
SINCE=$(git log -1 --format=%cI "$LAST_TAG")
gh pr list --state merged --base main --search "merged:>=$SINCE" \
  --limit 200 --json number,title,body,author,mergedAt
```

That is the list of PRs merged since the last release. The previous release PR
(`chore: release v…`) can show up in it; ignore it.

Read every PR's title and description, not just the title — the description is
where the author wrote what a user should know, and any operator notes.

Also read `CHANGELOG.md`. If an `## [Unreleased]` section with entries is still
there (from before the changelog moved into the release PR), those entries ship
in this release too.

Drop `chore`, `ci`, `test` and `build` PRs: they change nothing a user of csm
sees. `refactor` and `style` rarely do either; keep one only if its description
names a visible effect. **If nothing is left, stop** — there is nothing to
release, and a version whose changelog says nothing is worse than no release at
all.

## Phase 3: Choose the version

`$LAST_TAG` is the current version.

If the user named a version or a bump type, use it. Otherwise propose one from
the *content* of what ships, not the PR count:

- A `feat` users can act on, or anything removed → **minor**
- Only `fix`, `perf` and internal changes → **patch**
- A `!` in a PR title, or any breaking change to the CLI or its flags →
  **major** (pre-1.0, still call it out)

State the reasoning in one line and confirm before continuing. Do not tag a
release the user hasn't agreed to.

## Phase 4: Write the changelog section

Edit `CHANGELOG.md` so that:

1. A new `## [X.Y.Z] - YYYY-MM-DD` section (today's date) sits directly above
   the previous version's section
2. It groups entries under the Keep a Changelog headings this file already uses
   — `### Added`, `### Changed`, `### Fixed`, `### Removed`, in that order (add
   `### Deprecated` or `### Security` only when a change calls for it)
3. Each entry is one or two lines of prose: what changed, and why a user cares.
   Write it from the PR's title and description, not by pasting the title. Leave
   the mechanism out; it lives in the code comments
4. An operator note from a PR description that a user upgrading must act on
   stays in the entry; anything only the maintainer needs does not
5. Entries from a leftover `[Unreleased]` section are folded into the new
   section under the same headings, and the `## [Unreleased]` heading is
   removed

Do not touch the older version sections — they are history.

## Phase 5: PR the changelog

1. `git checkout -b chore/release-vX.Y.Z`
2. Commit `CHANGELOG.md` only, with `chore: release vX.Y.Z` as the whole
   message — no body, no trailers; the section it adds says the rest
3. `git push -u origin chore/release-vX.Y.Z`
4. `gh pr create` — title `chore: release vX.Y.Z`, body = the new section's
   entries, so the PR shows exactly what ships and the user can edit the prose
   before it becomes the release notes
5. Report the PR URL, then **stop**. `main` is protected; the tag must point at
   a merged commit, so the user merges before anything is tagged.

## Phase 6: Tag (only after the user confirms the merge)

```bash
git checkout main && git pull
grep -n '^## \[X.Y.Z\]' CHANGELOG.md
git tag vX.Y.Z
git push origin vX.Y.Z
```

Confirm `git log --oneline -1` is the release commit and the `grep` finds the
section before tagging — a tag on the wrong commit means re-cutting the
release, and a tag without its section fails the workflow.

## Phase 7: Verify the release landed

The workflow takes ~1-2 minutes. Wait for it, then check all four outcomes —
the Homebrew step has broken silently before, so binaries existing is not proof
the release worked:

```bash
until [ "$(gh run list --workflow=release.yaml --limit 1 --json status --jq '.[0].status')" = "completed" ]; do sleep 10; done
gh run list --workflow=release.yaml --limit 1

# 9 assets: darwin/linux × amd64/arm64, .deb ×2, .rpm ×2, checksums.txt
gh release view vX.Y.Z --json assets --jq '.assets[].name'

# Release notes must be the CHANGELOG section, not a PR list
gh release view vX.Y.Z --json body --jq .body

# Formula version must match the tag
gh api repos/yepzdk/homebrew-tools/contents/Formula/csm.rb --jq '.content' | base64 -d | grep '^  version'

```

Report the release URL, the asset count, and the formula version. If the run
failed at "Extract release notes", the tag has no `## [X.Y.Z]` section in
`CHANGELOG.md`. If the formula is stale, the run's "Update Homebrew formula"
step is where to look — usually an expired `HOMEBREW_TAP_PAT` (see `CLAUDE.md`).
