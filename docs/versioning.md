# Versioning and Release Management

This guide explains how the SDLC plugin manages project versions, automates releases through CI, and gives you control over version bumps through PRs.

## Three Independent Release Paths

Versioning is configured in `.sdlc-v2/config.toml` under the `version` key. This is the config location the Go-side tool `pr_prepare` reads for its version diagnostics, along with the CI scripts (`scaffold_ci` scaffolds those CI workflow files and does not itself read or write this config). `pr_apply` does not read the version config.

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

### Config and State Location in Git Worktrees

`.sdlc-v2/` state and config are read from the **main** git worktree root,
never whichever worktree you happen to be running a command from (the one
exception — files that setup and `scaffold_ci` write — is listed under
"Files written to the active worktree" below). This matters for repos
that use linked worktrees (e.g. a bare anchor repo with one or more linked
checkouts): reading or writing `.sdlc-v2/` from the wrong worktree would
silently split config and pipeline state across checkouts.

Two failure modes are checked for and surfaced via
`validate({action: "worktree_anchoring"})`:

- **`WORKTREE_ANCHOR_BARE`** (error) — the resolved main worktree is itself
  a bare repository (no working tree). This should not happen — worktree
  resolution skips bare entries when scanning `git worktree list` — but is
  checked as a regression guard.
- **`WORKTREE_ANCHOR_MISMATCH`** (warning) — `.sdlc-v2/` exists under the
  active worktree but not the main one, meaning config/state reads and
  writes are currently split across worktrees.

The check's output also reports `mainRoot`, `activeRoot`, `isLinked`,
`isBare`, `stateDir`, and `stateDirOwner` (`"main"` or `"active"`) for
diagnosis.

#### Files written to the active worktree

`setup_init`, `setup_write_sections` and `scaffold_ci` write git-tracked
files into the worktree you run them from, so the change lands on your branch:

| File | Written under |
|---|---|
| `.sdlc-v2/config.toml` | active worktree |
| `.sdlc-v2/.gitignore`, `.gitignore` (managed blocks) | active worktree |
| `.sdlc-v2/plan-template.md`, `.sdlc-v2/pr-template.md` | active worktree |
| `.github/scripts/*`, `.github/workflows/*` | active worktree |
| `.sdlc-v2/local.toml`, `.sdlc-v2/runs/` | main worktree (gitignored, shared) |

Other tools still read `config.toml` from the main worktree, so a config
change made in a linked worktree takes effect after the branch merges.

## Breaking Config Change

The pre-redesign flat shape (top-level `mode`, string `versionFile`, `changelogMethod`, boolean `changelog`, `rcAutoContinue`, `ticketPrefix`) is not parsed — there is no backward-compatible parsing and no automatic migration. `release-on-main.cjs` fails with a hard error naming `/setup --only version` as the fix. The Go config reader also fails on it, but `pr_prepare` treats that failure as "no version config": it returns no version diagnostics (no `versionConfig`, `bumpOptions`, or `conventionalSummary`) and raises no error or warning. `pr_apply` does not read the version config, so it neither rejects nor detects the old shape. Run `/setup --only version` to migrate. `ticketPrefix` in particular is dropped entirely — no CI script ever read it, and there is no replacement field.

## How Releases Work

Releases are **never created during the ship pipeline**. Version diagnostics (release readiness, bump level, and notes) are computed during PR preparation and validation. Actual version bumps, tags, and changelog writes happen **post-merge via CI**.

When you run `/ship`, the resolved `[version]` config section is snapshotted into the pipeline's saved state file at flag-resolution time (for diagnosability, not because a release happens there) — see [`skills/ship.md`](skills/ship.md#state-and-resolution-trace) for exactly what's recorded and how to read it back.

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
   - release:<level> label (e.g., release:minor); re-running pr_apply replaces any earlier release:* label
   - <!-- release-level:minor --> marker in PR body
   - <!-- release-notes-start/end --> markers with drafted notes under "## [Unreleased]" (no version number)
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
```

### CI Workflows

Four CI scripts handle the release pipeline. All live under `.github/scripts/` and are scaffolded by running `scaffold_ci`:

| Script | Trigger | Purpose |
|---|---|---|
| `release-on-main.cjs` | push to main | Runs the 4-phase release flow above: file writes, tag (never blocked), and PR delivery |
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
| `"push-with-secret"` (deprecated) | Alias for `"push"` — `release-on-main.cjs` and `promote-release.cjs` normalize it to `"push"` before running. The secret-name rewrite it used to gate is now independent of `method`; see "`push-with-secret` (deprecated)" below. |

There is no `"skip"` value — to skip a path entirely, disable it (`versionFile.enabled: false` / `changelog.enabled: false`) rather than routing its delivery through a no-op method.

Delivery runs after the tag and GitHub Release already exist (phase 3 above, when `tag.enabled`) and is always best-effort for the file-writing paths: a delivery failure is logged and reported in the final exit status, but never undoes a tag or release already created. See "Protected branches and rulesets" below for how `"push"` and `"pr"` behave under branch protection/rulesets, and for the token chain every scaffolded workflow uses.

## Protected branches and rulesets

### Why the default token fails

Every scaffolded release workflow (`release-on-main.yml`, `promote-release.yml`)
ends by pushing a release commit and/or a tag. On an
unprotected `main` that needs nothing special — the workflow's default
`secrets.GITHUB_TOKEN` pushes directly.

A GitHub ruleset or classic branch protection rule on `main` — or a tag
ruleset covering the release tag pattern — changes that. The default token
has no path around either kind of rule: it cannot receive an admin-override
on classic protection, and it cannot be added as a bypass actor on a
ruleset. Any push it makes to a protected ref is rejected with `GH013:
Repository rule violations` (classic protection instead returns a plain
permission error) — regardless of `version.method`, since even `"pr"` still
pushes the release tag directly when `tag.enabled` is true.

### How the workflows pick a token

`release-on-main.yml` and `promote-release.yml` both
resolve their push/`gh` credential through the same 3-way fallback chain,
tried in this order:

| Order | Source | Used when |
|---|---|---|
| 1 | App token (`steps.release-token.outputs.token`) | Repo variable `RELEASE_APP_CLIENT_ID` is set — a short-lived token is minted via `actions/create-github-app-token`, authenticated with secret `RELEASE_APP_PRIVATE_KEY` |
| 2 | `secrets.RELEASE_TOKEN` | No App token was minted, and repo secret `RELEASE_TOKEN` exists |
| 3 | `secrets.GITHUB_TOKEN` | Neither of the above — the workflow's own default token |

The same expression appears wherever a token is needed — the `checkout`
step's `token:` input and the release script's `GH_TOKEN` env var:

```yaml
token: ${{ steps.release-token.outputs.token || secrets.RELEASE_TOKEN || secrets.GITHUB_TOKEN }}
```

The App-token step (`id: release-token`) only runs when
`vars.RELEASE_APP_CLIENT_ID != ''`; with no App configured, the chain falls
straight from step 1 to step 2.

### Per-repo setup checklist

1. Check whether a ruleset or classic branch protection rule already covers
   `main`, and whether a tag ruleset covers your release tag pattern (e.g.
   `v*`). If neither exists, skip the rest of this checklist — the default
   token works as-is.
2. Choose one identity to authenticate release pushes: a GitHub App
   installed on this repo, or a fine-grained personal access token (PAT)
   belonging to a user whose role is exempt from the rule.
3. For a GitHub App: grant it `Contents`, `Pull requests`, and `Actions`
   read/write, install it on this repository, then set repo variable
   `RELEASE_APP_CLIENT_ID` and repo secret `RELEASE_APP_PRIVATE_KEY`. For a
   PAT: create a fine-grained PAT with the same scopes and store it as repo
   secret `RELEASE_TOKEN` (or another name, configured under
   `[version.pushAuth]` — see the deprecated section below).
4. Add that App, or the PAT-owning user, as a bypass actor on every ruleset
   that covers `main` and on every ruleset that covers your release tag
   pattern. Classic branch protection instead needs the automation identity
   exempted from the rule directly (an admin-override alone may not cover
   it).
5. Run `scaffold_ci` (or `/setup`) to install/refresh the workflows and
   their token chain, and check its `protection` report for what it still
   sees configured.
6. Trigger a real release, or dispatch **SDLC Promote Release**, and confirm
   the push and tag steps actually succeed in the run log — not just that
   the job finished.

### Recommended ruleset

Scope the ruleset to the default branch specifically, require the
release-relevant status check, and add your chosen bypass actor:

```json
{
  "name": "protect-main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] }
  },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "required_status_checks",
      "parameters": {
        "required_status_checks": [{ "context": "verify-release-intent" }],
        "strict_required_status_checks_policy": false
      }
    }
  ],
  "bypass_actors": [
    { "actor_type": "Integration", "actor_id": 123456, "bypass_mode": "always" }
  ]
}
```

Replace `actor_id` with your GitHub App's ID (or use
`actor_type: "RepositoryRole"` / `"Team"` for a PAT-based bypass). Add a
second ruleset with `"target": "tag"` and a matching `ref_name` pattern
(e.g. `v*`) if `tag.enabled` is true, with the same `bypass_actors` entry.

### Do not

- Do not scope `required_status_checks` to `~ALL` branches. Require them on
  `~DEFAULT_BRANCH` only — the release commit and tag both land on `main`
  itself, and scoping to every branch adds required checks unrelated to
  release pushes.
- Do not assume classic branch protection's admin-override covers your
  automation identity. Verify the App or PAT-owning user is actually exempt
  from the rule you added — an admin-override that excludes them still
  rejects the push.
- Do not protect `main` without also covering release tags. A branch
  ruleset alone stops the file-writing commit; if `tag.enabled` is true, the
  tag push (`refs/tags/...`) needs the same bypass actor on a matching tag
  ruleset, or it fails on its own.

### Using `method = "pr"` under rulesets

`method = "pr"` writes the release commit to a `release/<tag>` branch and
opens a PR into `main` labeled `no-release`, with auto-merge requested — it
never pushes to `main` directly, so a branch ruleset does not block it. It
does not exempt you from the setup checklist above: with `tag.enabled` true,
the tag is still pushed directly (`git push origin refs/tags/<tag>`, then
`gh release create`), so a tag ruleset still needs a bypass identity.
`promote-release.cjs`'s promotion bump follows the same rule: `deliverBump`
pushes to `release/<targetTag>` and opens a PR (`gh pr create --label
no-release --base <branch>`) when `method` is `"pr"`, with auto-merge
failures treated as a non-fatal warning.

Auto-merge itself needs "Allow auto-merge" enabled in Settings → General,
and no required check that never runs against the release PR — otherwise the
PR sits open and must be merged by hand; the tag and release from phase 3,
when `tag.enabled` is true, are created independently and do not wait on
that PR.

### `push-with-secret` (deprecated)

`push-with-secret` is a deprecated alias for `"push"`. `release-on-main.cjs`
and `promote-release.cjs` — the two scripts that read `method` — normalize
it before running:

```js
const method = config.method === 'push-with-secret' ? 'push' : (config.method || 'push');
```

What `push-with-secret` used to gate — rewriting the scaffolded workflows'
`secrets.RELEASE_TOKEN` reference to a custom secret name — no longer
depends on `method` at all: `scaffold_ci` rewrites `secrets.RELEASE_TOKEN`
to `secrets.<pushAuth.secretName>` in `release-on-main.yml` and
`promote-release.yml` (the workflow files, not the
`method` value) whenever `[version.pushAuth] secretName` is set to something
other than the default `RELEASE_TOKEN` — for any `method` value, including
`"push"` and `"pr"`.

If your config still has `method = "push-with-secret"`, it keeps working (as
`"push"`), but migrate it to `method = "push"` when convenient — the value
only exists for config files written before this change. If you're only
setting a custom push-auth secret name, configure `[version.pushAuth]`
directly; `method` no longer needs to be `"push-with-secret"` to activate
the rewrite:

```toml
[version]
method = "push"

[version.pushAuth]
secretName = "RELEASE_TOKEN"
```

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
- **Re-run `/pr`** (or `pr_apply`) with a new level: it removes the old `release:*` label and adds the new one, so the PR never has two

**PR body markers** (injected automatically, validated by CI):
```html
<!-- release-level:minor -->
<!-- release-notes-start -->
## [Unreleased]
### Added
- New feature X
### Fixed
- Bug Y
<!-- release-notes-end -->
```

The PR never contains a version number. At merge time, `release-on-main.cjs` computes it from
the tags present at that moment: the next `<level>` bump over the current version, and for an RC
the next free `-rcN`. A release that lands on the default branch while the PR is open does not
make the PR stale.

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

Alternatively, configure `version.preReleasePolicy: "always-rc"` in `.sdlc-v2/config.toml` to enforce RC bumps automatically in the `/ship` pipeline without needing the `--rc` flag. See the [Configuration Reference](#configuration-reference) table below for details.

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

When testing is complete, promote the latest RC to a final release without rebuilding:

1. Go to **Actions** → **SDLC Promote Release** workflow
2. Choose the bump level: `patch`, `minor`, or `major`. The workflow bumps the latest stable tag by this level to get the final version.
3. The workflow (`promote-release.cjs`) will:
   - Run `git fetch --tags --force` to ensure all remote tags are present
   - Find the latest RC tag of the active RC series (e.g., `v1.3.0-rc3` — the highest RC number)
   - Verify the final tag does not already exist (prevents duplicate promotions)
   - Create the final version tag at the **exact commit** where that RC tag points (no rebuild, no new commit at HEAD)
   - Bump the version file (when `versionFile.enabled`) and prepend the changelog entry (when `changelog.enabled`) to the current branch HEAD as bookkeeping
   - Create a non-pre-release GitHub Release with notes aggregated from all RC releases, deduplicated and labeled per-RC

| Final version compared to the RC series | Result |
|---|---|
| Equal | Tags the final version at the latest RC commit. |
| Higher (RC `1.3.1-rcN`, stable `1.3.0`, level `minor` gives `v1.4.0`) | Prints a `NOTICE:` line, then tags the higher version at the latest RC commit. Release notes come from the RC series. |
| Lower | Fails before any git write and names the level to use. |
| RC series not above the latest stable tag | Fails with `Nothing to promote`. |

The final release tags the exact commit that was tested as the RC — what you tested is what ships, no rebuild.

## Configuration Reference

Full `.sdlc-v2/config.toml` `version` section:

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
    },
    "pushAuth": {
      "secretName": "RELEASE_TOKEN"
    }
  }
}
```

| Field | Required | Default | Description |
|---|---|---|---|
| `preRelease` | No | — | Default pre-release label (e.g., `"rc"`). Overrides the resolved bump when the bump did not come from a CLI `--bump` flag (i.e., overrides config `ship.bump` and the built-in default, but not an explicit CLI flag). |
| `preReleasePolicy` | No | `"continue-rc"` | Controls RC suggestion and enforcement. `"always-rc"`: enforces RC bumps in `/ship` (overrides resolved bump to `"rc"` regardless of source, including an explicit CLI `--bump`); standalone `/pr` only suggests RC, it does not enforce. `"default-rc"`: suggests RC in `/pr` and makes `/ship` resolve the bump to `"rc"` by default, but an explicit CLI `--bump` overrides it to a final release. `"continue-rc"`: suggests RC only when existing RC tags are found (no ship-time enforcement). `"never"`: never suggests RC. |
| `method` | No | `"push"` | How the `versionFile` and `changelog` paths deliver their writes: `"push"` (direct commit to the default branch), `"pr"` (via a single `release/<tag>` PR — works with branch protection), or `"push-with-secret"` (deprecated alias for `"push"` — see "`push-with-secret` (deprecated)" under [Protected branches and rulesets](#protected-branches-and-rulesets)). Does not affect `tag`, which always pushes directly. |
| `tag.enabled` | No | `false` | Whether the tag path is active: creates a git tag and GitHub Release on every bump. |
| `tag.prefix` | No | auto-detected from existing tags; `/setup` writes `"v"` explicitly for new tag-only projects | Prefix for git tags (e.g., `v` for `v1.2.3`). |
| `versionFile.enabled` | No | `false` | Whether the version-file path is active. Also determines whether the current version is read from this file (`true`) or derived from git tags (`false`), independent of `tag.enabled`. |
| `versionFile.path` | Required if `versionFile.enabled` | auto-detected | Relative path to the version file. |
| `versionFile.fileType` | Required if `versionFile.enabled` | inferred | Parser to use for the version file: `package.json`, `cargo.toml`, `pyproject.toml`, `pubspec.yaml`, `plugin.json`, or `version-file`. |
| `changelog.enabled` | No | `false` | Whether the changelog path is active: prepends a release entry on every bump. |
| `changelog.file` | No | `"CHANGELOG.md"` (used only when `changelog.enabled`) | Path to changelog file. |
| `pushAuth.secretName` | No | `""` (effectively `RELEASE_TOKEN`) | Name of the repo secret holding the App or PAT token used in place of the default `RELEASE_TOKEN` fallback for release pushes. When set to a value other than `RELEASE_TOKEN`, `scaffold_ci` rewrites the scaffolded workflows' `secrets.RELEASE_TOKEN` reference to this name, for any `method`. See [Protected branches and rulesets](#protected-branches-and-rulesets). |

At least one of `tag.enabled` or `versionFile.enabled` must be `true` — a config with both false (or absent) is rejected by `release-on-main.cjs`. `pr_prepare` treats it as "no version config" and returns no version diagnostics; `pr_apply` does not read the version config.

## Release Workflow Automation

The plugin provides two workflows for automated releases:

### release-on-main.cjs (Push to main)

Triggered automatically on every push to `main`. Implements the 4-phase release flow:
1. Find the merged PR and its `release:<level>` label
2. Bump version file and changelog (skipped for RC)
3. Create git tag and GitHub Release (never blocked by step 2)
4. Deliver file writes via PR (when `method: "pr"`, skipped for RC)

**Tag resolution:** When `tag.enabled` is true, the tag is created at the current commit (typically the merge commit). The tag is pushed to remote and a GitHub Release is created.

### release-dispatch.yml (Release Dispatch workflow)

A companion workflow that listens for `release-on-main` completion. Its purpose is to dispatch further CI actions (e.g., GoReleaser binary builds, deployments) triggered only by final (non-RC) releases.

**Tag resolution (hardened):**
The workflow resolves the final release tag on HEAD by:
1. Fetching all tags (`fetch-tags: true`)
2. Sorting tags by semver version in descending order (`--sort=-version:refname`)
3. Filtering to exclude pre-release (RC) tags (`grep -v -- '-rc'`)
4. Taking the first match — the highest final release version

This ensures only actual releases (not RCs) trigger downstream automation like binary builds or deployments. RC tags are created by `release-on-main.cjs` but are intentionally skipped by `release-dispatch.yml`.

## Troubleshooting

### "GH013: Repository rule violations" on a release push

A branch or tag ruleset rejected the push — the release commit, the release
tag, or both — because it came from a token that isn't on the ruleset's
bypass list. `release-on-main.cjs` and `promote-release.cjs` each recognize
this failure (`GH013`, "Repository rule violations", or "protected branch"
in the git error) through their own push helper, and both prepend the same
hint to the workflow log:

```
Push rejected by a branch/tag ruleset: the pushing identity is not on its bypass list
(GITHUB_TOKEN can never bypass rulesets).
If a GitHub App or PAT is already configured: add that App or user to the bypass list of
every ruleset covering this ref, and check that the PAT has not expired.
Otherwise fix one of:
  1. Set repo variable RELEASE_APP_CLIENT_ID + secret RELEASE_APP_PRIVATE_KEY for a GitHub App
     that is on the ruleset bypass list.
  2. Set secret RELEASE_TOKEN to a fine-grained PAT of a user on the bypass list.
  3. Use version.method = "pr" (release commits go through a PR).
Docs: https://github.com/rnagrodzki/sdlc-plugin/blob/main/docs/versioning.md#protected-branches-and-rulesets
```

Option 2 names the configured `version.pushAuth.secretName` when one is set
(the secret the scaffolded workflow actually reads). Option 3 is left out
when the rejected ref is a tag — `method = "pr"` only reroutes the version
bump commit, never the tag push.

Follow the "Per-repo setup checklist" under [Protected branches and
rulesets](#protected-branches-and-rulesets) to configure one of the first
two options, or switch to `method = "pr"` — remembering that `"pr"` still
needs a bypass identity for the tag push when `tag.enabled` is true and a
tag ruleset covers it.

### "config: version section uses the old flat shape"

The project's `.sdlc-v2/config.toml` still has a `version` section in the pre-redesign flat shape (`mode`, string `versionFile`, `changelogMethod`, boolean `changelog`, or `rcAutoContinue`). Run `/setup --only version` to migrate to the new nested `tag`/`versionFile`/`changelog` shape — there is no automatic migration.

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

Not an issue in the current flow. `release-on-main.cjs` runs on push to main (after any merge, including squash merge) and creates the tag directly at HEAD (phase 3), so there is no pre-merge tag for a squash merge to orphan.

### verify-release-intent fails with "No version config found"

The scaffolded CI scripts (`release-on-main.cjs`, `verify-release-intent.cjs`, `promote-release.cjs`, `check-changelog.cjs`) read version config exclusively from `.sdlc-v2/config.toml`, matching the Go-side tool `pr_prepare`. There is no legacy fallback — a project still on the old `.sdlc/config.json` (or `.claude/sdlc.json` / `.claude/version.json`) layout must run the `migrate` tool first.

If `.sdlc-v2/config.toml` is missing or has no `.version` section, release automation fails with:

```
No version config found (.sdlc-v2/config.toml ".version" section). A release:* label requires a version config to compute the release target.
```

**Fix:** run `/setup` (or the `migrate` tool, if this project still has a legacy config) to create `.sdlc-v2/config.toml` with a `version` section.
