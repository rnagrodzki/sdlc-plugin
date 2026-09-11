# Versioning and Release Management

This guide explains how the SDLC plugin manages project versions, automates releases through CI, and gives you control over version bumps through PRs.

## Three Independent Release Paths

Versioning is configured in `.sdlc-v2/config.json` under the `version` key. This is the config location the Go-side tools `pr_prepare` and `pr_apply` read, along with the CI scripts (`scaffold_ci` scaffolds those CI workflow files and does not itself read or write this config).

A release is made of three independently toggleable paths, each carrying its own `enabled` flag:

| Path | What it does | Config sub-object |
|---|---|---|
| **tag** | Creates a git tag (and GitHub Release) at the release commit. | `version.tag` |
| **versionFile** | Bumps the version string inside a tracked file. | `version.versionFile` |
| **changelog** | Prepends a release entry to a changelog file. | `version.changelog` |

Each path is on or off independently — there is no single "mode" governing all three. A missing sub-object means that path is disabled; there is no ambiguous default. The three paths share only two things: the bump policy (`preRelease`, `preReleasePolicy`) and, for `versionFile`/`changelog`, a delivery `method` (`push` or `pr`) that controls how their writes land on the default branch. `tag` always pushes directly — it is never blocked by `method` or by the other two paths failing.

**At least one of `tag.enabled` or `versionFile.enabled` must be `true`.** A version section where both are false (or absent) is rejected — `changelog` alone has nothing to determine a release version from.

**Which path determines the "current version"?** `versionFile.enabled` — not `tag.enabled` — decides where the current version is read from:
- `versionFile.enabled: true` — the current version comes from the configured file (`versionSource.type` is `"file"`).
- `versionFile.enabled: false` — the current version is derived from the highest semver git tag instead (`versionSource.type` is `"tag"`, `path` is empty). When no matching tags exist yet, the version defaults to `0.0.0`.

This holds regardless of whether `tag.enabled` is true — a project can read its version from a file while never creating tags, or derive its version from tags while never writing a file.

### Example: file + tag + changelog, delivered via PR

```json
{
  "version": {
    "preReleasePolicy": "continue-rc",
    "method": "pr",
    "tag": {
      "enabled": true,
      "prefix": "v"
    },
    "versionFile": {
      "enabled": true,
      "path": "package.json",
      "fileType": "package.json"
    },
    "changelog": {
      "enabled": true,
      "file": "CHANGELOG.md"
    }
  }
}
```

### Example: tag-only, no version file, no changelog

```json
{
  "version": {
    "method": "push",
    "tag": {
      "enabled": true,
      "prefix": "v"
    }
  }
}
```

`versionFile` and `changelog` are simply omitted — both default to `{enabled: false}`.

**Supported `versionFile.fileType` values:**

| File | `fileType` value | How version is stored |
|---|---|---|
| `package.json` | `"package.json"` | `"version": "1.2.3"` JSON field |
| `plugin.json` | `"plugin.json"` | `"version": "1.2.3"` JSON field |
| `Cargo.toml` | `"cargo.toml"` | `version = "1.2.3"` under `[package]` |
| `pyproject.toml` | `"pyproject.toml"` | `version = "1.2.3"` under `[tool.poetry]` or `[project]` |
| `pubspec.yaml` | `"pubspec.yaml"` | `version: 1.2.3` top-level key |
| `VERSION` | `"version-file"` | Plain text, first line |

When `versionFile.enabled` is true and `versionFile.path` is omitted, the plugin auto-detects by probing the project root in the order listed above.

### Setup

Run `/setup` to auto-detect and configure versioning. The setup skill:
- Probes for known version files (package.json, Cargo.toml, etc.)
- If found: proposes `versionFile.enabled: true` with the detected path and type, plus `tag.enabled: true`
- If none found: proposes `tag.enabled: true` with `tag.prefix: "v"` and `versionFile` left disabled

You can also write the config manually, or run `/setup --only version` to reconfigure just this section (also the required path to migrate an old flat-shape `version` section — see "Breaking Config Change" below).

## Breaking Config Change

The pre-redesign flat shape (top-level `mode`, string `versionFile`, `changelogMethod`, boolean `changelog`, `rcAutoContinue`, `ticketPrefix`) is **rejected outright** by every reader (`pr_prepare`, `pr_apply`, and every CI script) — there is no backward-compatible parsing and no automatic migration. A project still on the old shape gets a hard error naming `/setup --only version` as the fix. `ticketPrefix` in particular is dropped entirely — no CI script ever read it, and there is no replacement field.

## How Releases Work

Releases are **never created during the ship pipeline**. Version diagnostics (release readiness, bump level, and notes) are computed during PR preparation and validation. Actual version bumps, tags, and changelog writes happen **post-merge via CI**.

### Pre-Merge Safety: verify-release-intent

**You will never have a broken release after merge.** The `verify-release-intent.cjs` CI check runs on every PR event and catches all release problems before merge:

| Check | What it catches |
|---|---|
| Missing release notes markers | PR body edited/corrupted after `pr_apply` |
| Empty release notes | Label applied manually without writing notes |
| Level mismatch | Label says `release:minor` but marker says `patch` |
| Missing RC marker | RC label applied without `<!-- release-pre:rc -->` |
| Version file unreadable | Corrupt or missing version file (when `versionFile.enabled`) |
| Target tag already exists | Version already released — prevents duplicate releases |
| No version config | Label applied to a project without version config |

If any check fails, the PR CI status blocks merge (when branch protection requires it). All failures are reported together — one CI run shows every problem, not one at a time.

This check validates any combination of enabled paths. When `versionFile.enabled` is false, it reads the current version from git tags instead (same as `release-on-main.cjs` does post-merge) and computes the target tag to verify it does not already exist.

### The Release Flow

```
1. Developer runs /ship (or /pr)
2. PR skill analyzes conventional commits since last tag
3. PR skill suggests bump level (major/minor/patch)
4. PR skill creates PR with:
   - release:<level> label (e.g., release:minor)
   - <!-- release-level:minor --> marker in PR body
   - <!-- release-notes-start/end --> markers with drafted notes
5. CI runs verify-release-intent.cjs on PR (pre-merge check)
6. PR is reviewed and merged
7. CI runs release-on-main.cjs on push to main (post-merge)
8. release-on-main.cjs runs 4 phases, tag creation never blocked by the others:

   PHASE 1 (read-only): find the merged PR, its release:* label, notes, and
     compute the bumped version.

   PHASE 2 (file writes — skipped entirely for RC releases):
     method "push": write the enabled versionFile/changelog files, commit
       "chore(release): <version>", push directly to main.
     method "pr": write the same files and commit locally (nothing pushed
       yet — phase 4 delivers it).
     Idempotent: a file write that produces no diff is treated as already
       up to date, not as a failure.

   PHASE 3 (tag — never blocked by phase 2 failures): if tag.enabled, tag
     the release commit from phase 2 (or, on re-run after a partial
     failure, a chore(release) commit already on origin, or the merge
     commit itself as last resort) and create a GitHub Release.

   PHASE 4 (PR delivery — only when method is "pr" and phase 2 wrote at
     least one file, skipped for RC): push the local commit from phase 2
     to a release/<tag> branch, open a PR labeled no-release, and enable
     auto-merge.

   Exit: every enabled path is attempted regardless of the others'
   outcome. Exit code is 0 only if every enabled path succeeded or was
   idempotently skipped; 1 if any failed.
9. retag-release.cjs also runs on push to main but is a no-op in this
   flow: the tag from phase 3 is already at HEAD, so there is nothing
   to move. It is deprecated (superseded by release-on-main.cjs) and
   kept only for backward compatibility with older workflows.
```

### CI Workflows

Five CI scripts handle the release pipeline. All live under `.github/scripts/` and are scaffolded by running `scaffold_ci`:

| Script | Trigger | Purpose |
|---|---|---|
| `release-on-main.cjs` | push to main | Runs the 4-phase release flow above: file writes, tag (never blocked), and PR delivery |
| `retag-release.cjs` | push to main | **Deprecated.** Legacy safety net superseded by `release-on-main.cjs`; no-op in the current flow |
| `verify-release-intent.cjs` | pull_request | Pre-merge check: validates release markers |
| `promote-release.cjs` | workflow_dispatch | Promotes RC to final release |
| `check-changelog.cjs` | push, pull_request | Push to main: fails if `changelog.enabled` and no changelog entry exists for the current version. PR: warns (never fails) if the changelog file was hand-edited on a feature branch |

Matching workflow files live under `.github/workflows/`.

**To scaffold CI workflows:** Run `/setup` which offers CI scaffolding, or call `scaffold_ci` directly. Scaffolding also runs a read-only branch protection check against the repo's rulesets/classic protection and reports the result — see below.

### Delivery Method (`method`)

Controls how the `versionFile` and `changelog` paths deliver their writes when either is enabled. Does not affect the `tag` path, which always pushes tags and creates GitHub Releases directly regardless of `method`.

| Value | Behavior |
|---|---|
| `"push"` (default) | Direct commit and push to main. Simple, but blocked by branch protection. |
| `"pr"` | Writes land on a single `release/<tag>` branch carrying both file writes, opened as a PR with auto-merge enabled. Works with branch protection. |

There is no `"skip"` value — to skip a path entirely, disable it (`versionFile.enabled: false` / `changelog.enabled: false`) rather than routing its delivery through a no-op method.

Delivery runs after the tag and GitHub Release already exist (phase 3 above, when `tag.enabled`) and is always best-effort for the file-writing paths: a delivery failure is logged and reported in the final exit status, but never undoes a tag or release already created. See "Branch Protection & Release Workflow" below for how `"push"` and `"pr"` behave on a protected `main`.

### Branch Protection & Release Workflow

GitHub branch protection (classic) and rulesets can block direct pushes to
the default branch, including from `github-actions[bot]`. Adding the bot to
a bypass list is often not possible: GitHub rejects `github-actions[bot]` in
a ruleset bypass actor list (HTTP 422), and the bot cannot be granted an
admin-override bypass on classic protection either. This affects
`method: "push"` specifically: a workflow step that runs
`git push origin HEAD:main` (the versionFile/changelog commit) fails outright on
a protected `main`. `"pr"` delivers the same commit through a PR
instead, which is unaffected because it never pushes to the protected
branch directly.

**Why the tag path is unaffected:** the release tag and GitHub Release are created
via the GitHub API (`gh release create`) and a tag ref push (`refs/tags/...`),
neither of which touches the protected branch — `tag.enabled` releases land
regardless of `method`. Only a `git push` of a commit directly to `main` — the
`"push"` method's file-writing commit — is blocked.

**The `"pr"` method:** instead of pushing the versionFile/changelog update straight to
`main`, after the tag and release are already created, it:

1. Creates a branch `release/<tag>` off the tip of `main` and pushes the local commit made during phase 2.
2. Opens a PR (`gh pr create --base main --head release/<tag>`) labeled
   `no-release`.
3. Enables auto-merge on the PR (`gh pr merge --auto --squash --delete-branch`).

The release PR does not trigger a duplicate release when it merges — see
"The `no-release` label" below for why.

File delivery — for both `"push"` and `"pr"` — is **best-effort and
non-blocking relative to the tag path**: the tag and GitHub Release from phase 3 of the release flow
above are created first (when `tag.enabled`) and are never rolled back if delivery fails for any
reason (missing `gh` auth, no push access, a protected `main` with
`method: "push"`, auto-merge not enabled on the repo, etc.) — though the overall
script still exits 1 to surface the failure. The
failure is logged to the workflow output.

**Auto-merge setup (`"pr"` only):** the target repo must have "Allow
auto-merge" enabled in Settings → General, and `main` must not require a
status check that never runs (auto-merge waits indefinitely for required
checks). If auto-merge cannot be enabled (e.g. required reviews with no
eligible reviewer), the release PR is still created — merge it manually.

**The `no-release` label:** applied to the automated release PR (`"pr"`
method) as a human-facing signal (it is not read by any script).
`verify-release-intent.cjs` independently skips any PR lacking a
`release:<level>` label — the release PR has no such label, so it is a
no-op there regardless.

**Troubleshooting:**

- **Release PR not created at all (`"pr"` method)** — check the
  `release-on-main` workflow run logs for a caught error near "Phase 4
  (PR delivery) failed"; the tag/release step above it succeeded regardless
  (if `tag.enabled`).
  Common causes: `gh` not authenticated in the workflow, or the workflow's
  `GITHUB_TOKEN` permissions don't include `contents: write` /
  `pull-requests: write`.
- **File-write commit rejected (`"push"` method)** — the branch is
  protected. Switch `method` to `"pr"` (works around protection), or
  remove the protection rule for the automation actor.
- **Release PR created but not merging (`"pr"` method)** — auto-merge is
  likely disabled repo-wide, or a required check on `main` is not
  configured to run on this PR. Merge it manually; this does not affect the
  already-published release.
- **`scaffold_ci` reports "branch protection detected"** — informational,
  and only actionable if `method` is `"push"`: that method's
  direct push will be blocked, so switch to `"pr"`. With
  `"pr"` configured, no bypass or rule change is required. Disabling
  `versionFile`/`changelog` entirely sidesteps this too, since only those
  two paths use `method`.

## Controlling Version Bumps via PRs

### Bump Levels

Version bumps follow semver. Given current version `1.2.3`:

| Level | Result | When to use |
|---|---|---|
| `patch` | `1.2.4` | Bug fixes, minor changes |
| `minor` | `1.3.0` | New features, backward-compatible |
| `major` | `2.0.0` | Breaking changes |

Version bumps are auto-suggested based on conventional commit prefixes:
- `feat:` commits → suggests `minor`
- `fix:` commits → suggests `patch`
- `BREAKING CHANGE:` or `feat!:`/`fix!:` → suggests `major`

You can override the suggestion by passing the level explicitly: `/pr --bump patch` or `/ship --bump minor`.

### PR Labels and Markers

When the PR skill creates a PR with release intent, it adds:

**Label:** `release:<level>` (e.g., `release:minor`, `release:patch-rc`)

The label is what CI reads to decide whether to create a release. No label = no release. You can:
- **Add a label manually** to trigger a release on an existing PR
- **Remove the label** to prevent a release
- **Change the label** to change the bump level (e.g., `release:minor` → `release:major`)

**PR body markers** (injected automatically, validated by CI):
```html
<!-- release-level:minor -->
<!-- release-notes-start -->
### Added
- New feature X
### Fixed
- Bug Y
<!-- release-notes-end -->
```

The `verify-release-intent.cjs` CI check validates that:
- Markers exist when a `release:*` label is present
- The marker level matches the label level
- The version file exists and is parseable (only when `versionFile.enabled`)
- The target version is not already tagged

### Editing Release Notes

Release notes live in the PR body between `<!-- release-notes-start -->` and `<!-- release-notes-end -->` markers. Edit them directly in the PR description before merging. CI reads the final PR body at merge time.

Do NOT edit the `<!-- release-level:... -->` marker — change the PR label instead.

## Release Candidates (RC)

RC releases let you publish a pre-release version for testing before committing to a final release.

### Creating an RC

Use the `--rc` flag: `/pr --bump minor-rc` or `/ship --bump minor-rc`.

Alternatively, configure `version.preReleasePolicy: "always-rc"` in `.sdlc-v2/config.json` to enforce RC bumps automatically in the `/ship` pipeline without needing the `--rc` flag. See the [Configuration Reference](#configuration-reference) table below for details.

This creates:
- Label: `release:minor-rc`
- Marker: `<!-- release-pre:rc -->`
- On merge: CI creates tag `v1.3.0-rc1` (auto-incremented RC number) as a GitHub pre-release (when `tag.enabled`)

RC releases skip phase 2 (file writes) entirely — the `versionFile` and `changelog` paths are never touched for an RC, regardless of whether they're enabled. Only the `tag` path runs. The version file stays at the pre-bump value until the final release.

### Multiple RCs

Each merge with a `release:<level>-rc` label creates the next RC number:
- First merge: `v1.3.0-rc1`
- Second merge: `v1.3.0-rc2`
- Third merge: `v1.3.0-rc3`

RC numbers are auto-detected from existing tags.

### Promoting RC to Final Release

When testing is complete, promote the latest RC to a final release:

1. Go to Actions → "Promote Release" workflow
2. Enter the target version (e.g., `v1.3.0`)
3. The workflow (`promote-release.cjs`) will:
   - Find the latest RC tag for that version (e.g., `v1.3.0-rc3`)
   - Create the final tag `v1.3.0` at the **same commit** as the RC (no rebuild)
   - Bump the version file (when `versionFile.enabled`) and prepend the changelog entry (when `changelog.enabled`)
   - Create a non-pre-release GitHub Release

The final release tags the exact commit that was tested as the RC.

## Configuration Reference

Full `.sdlc-v2/config.json` `version` section:

```json
{
  "version": {
    "preRelease": "rc",
    "preReleasePolicy": "continue-rc",
    "method": "push",
    "tag": {
      "enabled": true,
      "prefix": "v"
    },
    "versionFile": {
      "enabled": true,
      "path": "path/to/version-file",
      "fileType": "package.json"
    },
    "changelog": {
      "enabled": false,
      "file": "CHANGELOG.md"
    }
  }
}
```

| Field | Required | Default | Description |
|---|---|---|---|
| `preRelease` | No | — | Default pre-release label (e.g., `"rc"`) applied when no explicit base bump or `--pre` is given. |
| `preReleasePolicy` | No | `"continue-rc"` | Controls RC suggestion and enforcement. `"always-rc"`: enforces RC bumps in `/ship` (overrides resolved bump to `"rc"` regardless of source when no explicit `preRelease` is set); standalone `/pr` only suggests RC, it does not enforce. `"continue-rc"`: suggests RC only when existing RC tags are found (no ship-time enforcement). `"never"`: never suggests RC. |
| `method` | No | `"push"` | How the `versionFile` and `changelog` paths deliver their writes: `"push"` (direct commit to the default branch), `"pr"` (via a single `release/<tag>` PR — works with branch protection). Does not affect `tag`, which always pushes directly. |
| `tag.enabled` | No | `false` | Whether the tag path is active: creates a git tag and GitHub Release on every bump. |
| `tag.prefix` | No | auto-detected from existing tags; `/setup` writes `"v"` explicitly for new tag-only projects | Prefix for git tags (e.g., `v` for `v1.2.3`). |
| `versionFile.enabled` | No | `false` | Whether the version-file path is active. Also determines whether the current version is read from this file (`true`) or derived from git tags (`false`), independent of `tag.enabled`. |
| `versionFile.path` | Required if `versionFile.enabled` | auto-detected | Relative path to the version file. |
| `versionFile.fileType` | Required if `versionFile.enabled` | inferred | Parser to use for the version file: `package.json`, `cargo.toml`, `pyproject.toml`, `pubspec.yaml`, `plugin.json`, or `version-file`. |
| `changelog.enabled` | No | `false` | Whether the changelog path is active: prepends a release entry on every bump. |
| `changelog.file` | No | `"CHANGELOG.md"` (used only when `changelog.enabled`) | Path to changelog file. |

At least one of `tag.enabled` or `versionFile.enabled` must be `true` — a config with both false (or absent) is rejected by `pr_prepare`, `pr_apply`, and every CI script.

## Troubleshooting

### "config: version section uses the old flat shape"

The project's `.sdlc-v2/config.json` still has a `version` section in the pre-redesign flat shape (`mode`, string `versionFile`, `changelogMethod`, boolean `changelog`, or `rcAutoContinue`). Run `/setup --only version` to migrate to the new nested `tag`/`versionFile`/`changelog` shape — there is no automatic migration.

### No release created after PR merge

Check:
1. Does the PR have a `release:*` label? No label = no release.
2. Are CI workflows scaffolded? Run `scaffold_ci` to add them.
3. Did `verify-release-intent.cjs` pass? Check the PR checks tab.
4. Are the `<!-- release-notes-start/end -->` markers in the PR body?

### Version file not bumped

Expected behavior when `versionFile.enabled` is false. CI derives the current version from git tags instead and — when `tag.enabled` — creates only a git tag and GitHub Release; no file is bumped.

### RC number unexpected

RC numbers are determined by scanning existing tags. If `v1.3.0-rc1` and `v1.3.0-rc2` exist, the next RC is `v1.3.0-rc3`. Deleting tags can cause gaps but not collisions.

### Tag prefix mismatch

If your tags use a prefix other than `v` (or no prefix), set `tag.prefix` accordingly. The plugin detects the prefix from existing tags, but explicit config is more reliable. Tags that do not match the prefix pattern are ignored.

### verify-release-intent not blocking merge

The CI check only blocks merge when GitHub branch protection requires it. To enable:
1. Go to repo Settings → Branches → Branch protection rules
2. Add rule for `main`
3. Enable "Require status checks to pass before merging"
4. Add `verify-release-intent` to required checks

Without this, the check runs but a failing result does not prevent merge.

### Squash merge breaks tag reachability

Not an issue in the current flow. `release-on-main.cjs` runs on push to main (after any merge, including squash merge) and creates the tag directly at HEAD (phase 3), so there is no pre-merge tag for a squash merge to orphan. `retag-release.cjs` is kept as a deprecated legacy safety net for projects still migrating from an older pre-merge-tag flow; when run, it detects the tag is already reachable from HEAD and does nothing.

### verify-release-intent fails with "No version config found"

The scaffolded CI scripts (`release-on-main.cjs`, `retag-release.cjs`, `verify-release-intent.cjs`, `promote-release.cjs`, `check-changelog.cjs`) read version config exclusively from `.sdlc-v2/config.json`, matching the Go-side tools `pr_prepare` and `pr_apply`. There is no legacy fallback — a project still on the old `.sdlc/config.json` (or `.claude/sdlc.json` / `.claude/version.json`) layout must run the `migrate` tool first.

If `.sdlc-v2/config.json` is missing or has no `.version` section, release automation fails with:

```
No version config found (.sdlc-v2/config.json ".version" section). A release:* label requires a version config to compute the release target.
```

**Fix:** run `/setup` (or the `migrate` tool, if this project still has a legacy config) to create `.sdlc-v2/config.json` with a `version` section.
