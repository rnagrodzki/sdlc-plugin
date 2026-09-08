# Versioning and Release Management

This guide explains how the SDLC plugin manages project versions, automates releases through CI, and gives you control over version bumps through PRs.

## Version Modes

The plugin supports two version tracking modes, configured in `.sdlc-v2/config.json` under the `version` key. This is the config location the Go-side tools `version_prepare` and `version_apply` read and write (`scaffold_ci` scaffolds CI workflow files and does not itself read or write this config).

### File Mode (default)

The version lives in a file on disk. The plugin reads and (via CI) writes to this file.

```json
{
  "version": {
    "mode": "file",
    "versionFile": "package.json",
    "fileType": "package.json",
    "tagPrefix": "v",
    "changelog": true,
    "changelogFile": "CHANGELOG.md"
  }
}
```

**Supported file types:**

| File | `fileType` value | How version is stored |
|---|---|---|
| `package.json` | `"package.json"` | `"version": "1.2.3"` JSON field |
| `plugin.json` | `"plugin.json"` | `"version": "1.2.3"` JSON field |
| `Cargo.toml` | `"cargo.toml"` | `version = "1.2.3"` under `[package]` |
| `pyproject.toml` | `"pyproject.toml"` | `version = "1.2.3"` under `[tool.poetry]` or `[project]` |
| `pubspec.yaml` | `"pubspec.yaml"` | `version: 1.2.3` top-level key |
| `VERSION` | `"version-file"` | Plain text, first line |

When `versionFile` is omitted, the plugin auto-detects by probing the project root in the order listed above.

**When to use file mode:** Your project already has a version file (most npm, Rust, Python, Flutter, and Claude plugin projects). The version file is the source of truth. CI bumps it on release.

### Tag Mode

The version is derived from git tags. No version file needed.

```json
{
  "version": {
    "mode": "tag",
    "tagPrefix": "v",
    "changelog": true,
    "changelogFile": "CHANGELOG.md"
  }
}
```

The plugin reads the highest semver git tag matching the configured `tagPrefix` (e.g., `v1.2.3`) and uses that as the current version. When no tags exist, the version defaults to `0.0.0`.

`versionFile` and `fileType` are not needed and are ignored in tag mode.

**When to use tag mode:** Your project tracks versions purely through git tags — Go modules, shell scripts, infrastructure repos, or any project where a version file adds no value. CI creates tags on release; no file is bumped.

### Setup

Run `/setup` to auto-detect and configure versioning. The setup skill:
- Probes for known version files (package.json, Cargo.toml, etc.)
- If found: writes `mode: "file"` with the detected file path and type
- If none found: writes `mode: "tag"` with `tagPrefix: "v"`

You can also write the config manually.

## How Releases Work

Releases are **never created during the ship pipeline**. The version skill only diagnoses release readiness and drafts a bump level + release notes. Actual version bumps, tags, and changelog writes happen **post-merge via CI**.

### Pre-Merge Safety: verify-release-intent

**You will never have a broken release after merge.** The `verify-release-intent.cjs` CI check runs on every PR event and catches all release problems before merge:

| Check | What it catches |
|---|---|
| Missing release notes markers | PR body edited/corrupted after `pr_apply` |
| Empty release notes | Label applied manually without writing notes |
| Level mismatch | Label says `release:minor` but marker says `patch` |
| Missing RC marker | RC label applied without `<!-- release-pre:rc -->` |
| Version file unreadable | Corrupt or missing version file (file mode) |
| Target tag already exists | Version already released — prevents duplicate releases |
| No version config | Label applied to a project without version config |

If any check fails, the PR CI status blocks merge (when branch protection requires it). All failures are reported together — one CI run shows every problem, not one at a time.

This check validates both `file` and `tag` modes. In tag mode, it reads the current version from git tags (same as `release-on-main.cjs` does post-merge) and computes the target tag to verify it does not already exist.

### The Release Flow

```
1. Developer runs /ship (or /version + /pr separately)
2. Version skill analyzes conventional commits since last tag
3. Version skill suggests bump level (major/minor/patch)
4. PR skill creates PR with:
   - release:<level> label (e.g., release:minor)
   - <!-- release-level:minor --> marker in PR body
   - <!-- release-notes-start/end --> markers with drafted notes
5. CI runs verify-release-intent.cjs on PR (pre-merge check)
6. PR is reviewed and merged
7. CI runs release-on-main.cjs on push to main (post-merge)
8. release-on-main.cjs:
   a. Finds the merged PR and its release:* label
   b. Reads release notes from PR body markers
   c. Computes the new version (bumps from current)
   d. File mode: bumps the version file, prepends CHANGELOG
   e. Tag mode: skips file bump (no version file)
   f. Creates annotated git tag directly at HEAD on main + GitHub Release
9. retag-release.cjs also runs on push to main but is a no-op in this
   flow: the tag from step 8f is already at HEAD, so there is nothing
   to move. It is deprecated (superseded by release-on-main.cjs) and
   kept only for backward compatibility with older workflows.
```

### CI Workflows

Four CI scripts handle the release pipeline. All live under `.github/scripts/` and are scaffolded by running `scaffold_ci`:

| Script | Trigger | Purpose |
|---|---|---|
| `release-on-main.cjs` | push to main | Creates release after PR merge; tags HEAD directly |
| `retag-release.cjs` | push to main | **Deprecated.** Legacy safety net superseded by `release-on-main.cjs`; no-op in the current flow |
| `verify-release-intent.cjs` | pull_request | Pre-merge check: validates release markers |
| `promote-release.cjs` | workflow_dispatch | Promotes RC to final release |

Matching workflow files live under `.github/workflows/`.

**To scaffold CI workflows:** Run `/setup` which offers CI scaffolding, or call `scaffold_ci` directly. The version skill also offers scaffolding for tag-mode projects when CI workflows are missing.

## Controlling Version Bumps via PRs

### Bump Levels

Version bumps follow semver. Given current version `1.2.3`:

| Level | Result | When to use |
|---|---|---|
| `patch` | `1.2.4` | Bug fixes, minor changes |
| `minor` | `1.3.0` | New features, backward-compatible |
| `major` | `2.0.0` | Breaking changes |

The version skill auto-suggests a level based on conventional commit prefixes:
- `feat:` commits → suggests `minor`
- `fix:` commits → suggests `patch`
- `BREAKING CHANGE:` or `feat!:`/`fix!:` → suggests `major`

You can override the suggestion by passing the level explicitly: `/version patch` or `/ship --bump minor`.

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
- The version file exists and is parseable (file mode only)
- The target version is not already tagged

### Editing Release Notes

Release notes live in the PR body between `<!-- release-notes-start -->` and `<!-- release-notes-end -->` markers. Edit them directly in the PR description before merging. CI reads the final PR body at merge time.

Do NOT edit the `<!-- release-level:... -->` marker — change the PR label instead.

## Release Candidates (RC)

RC releases let you publish a pre-release version for testing before committing to a final release.

### Creating an RC

Use the `--rc` flag: `/version minor --rc` or `/ship --bump minor-rc`.

This creates:
- Label: `release:minor-rc`
- Marker: `<!-- release-pre:rc -->`
- On merge: CI creates tag `v1.3.0-rc1` (auto-incremented RC number) as a GitHub pre-release

RC releases do NOT bump the version file or update the CHANGELOG. The version file stays at the pre-bump value until the final release.

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
   - Bump the version file (file mode) and prepend CHANGELOG
   - Create a non-pre-release GitHub Release

The final release tags the exact commit that was tested as the RC.

## Configuration Reference

Full `.sdlc-v2/config.json` `version` section:

```json
{
  "version": {
    "mode": "file | tag",
    "versionFile": "path/to/version-file",
    "fileType": "package.json | plugin.json | cargo.toml | pyproject.toml | pubspec.yaml | version-file",
    "tagPrefix": "v",
    "changelog": true,
    "changelogFile": "CHANGELOG.md",
    "ticketPrefix": "PROJ-",
    "preRelease": "rc"
  }
}
```

| Field | Required | Default | Description |
|---|---|---|---|
| `mode` | No | `"file"` | `"file"` reads version from a file; `"tag"` reads from git tags |
| `versionFile` | File mode | auto-detect | Relative path to version file |
| `fileType` | File mode | inferred | Parser to use for the version file |
| `tagPrefix` | No | auto-detected from existing tags; `/setup` writes `"v"` explicitly for new tag-mode projects | Prefix for git tags (e.g., `v` for `v1.2.3`) |
| `changelog` | No | `false` | Whether to maintain a CHANGELOG file |
| `changelogFile` | No | `"CHANGELOG.md"` (used only when `changelog` is `true`) | Path to changelog file |
| `ticketPrefix` | No | — | Jira ticket prefix for linking (e.g., `"PROJ-"`) |
| `preRelease` | No | — | Default pre-release label (e.g., `"rc"`) |

## Troubleshooting

### "mode 'tag' is not yet supported"

Older versions of the plugin did not support tag mode. Update the plugin to the latest version.

### No release created after PR merge

Check:
1. Does the PR have a `release:*` label? No label = no release.
2. Are CI workflows scaffolded? Run `scaffold_ci` to add them.
3. Did `verify-release-intent.cjs` pass? Check the PR checks tab.
4. Are the `<!-- release-notes-start/end -->` markers in the PR body?

### Version file not bumped (tag mode)

Expected behavior. Tag-mode projects do not have a version file. CI creates only a git tag and GitHub Release.

### RC number unexpected

RC numbers are determined by scanning existing tags. If `v1.3.0-rc1` and `v1.3.0-rc2` exist, the next RC is `v1.3.0-rc3`. Deleting tags can cause gaps but not collisions.

### Tag prefix mismatch

If your tags use a prefix other than `v` (or no prefix), set `tagPrefix` accordingly. The plugin detects the prefix from existing tags, but explicit config is more reliable. Tags that do not match the prefix pattern are ignored.

### verify-release-intent not blocking merge

The CI check only blocks merge when GitHub branch protection requires it. To enable:
1. Go to repo Settings → Branches → Branch protection rules
2. Add rule for `main`
3. Enable "Require status checks to pass before merging"
4. Add `verify-release-intent` to required checks

Without this, the check runs but a failing result does not prevent merge.

### Squash merge breaks tag reachability

Not an issue in the current flow. `release-on-main.cjs` runs on push to main (after any merge, including squash merge) and creates the tag directly at HEAD, so there is no pre-merge tag for a squash merge to orphan. `retag-release.cjs` is kept as a deprecated legacy safety net for projects still migrating from an older pre-merge-tag flow; when run, it detects the tag is already reachable from HEAD and does nothing.

### verify-release-intent fails with "No version config found"

The scaffolded CI scripts (`release-on-main.cjs`, `retag-release.cjs`, `verify-release-intent.cjs`, `promote-release.cjs`) currently read version config from `.sdlc/config.json` only, falling back to the legacy `.claude/sdlc.json` / `.claude/version.json` locations. They do **not** read `.sdlc-v2/config.json`, even though the Go-side tools `version_prepare` and `version_apply` read and write `.sdlc-v2/config.json` exclusively.

On a project set up by the current `/setup` (which writes `.sdlc-v2/config.json`), the CI scripts find no config and release automation fails with:

```
No version config found (.sdlc/config.json ".version" section, or legacy .claude/sdlc.json / .claude/version.json)
```

**Workaround:** until the CI scripts are updated to read `.sdlc-v2/config.json`, also mirror your version config at `.sdlc/config.json`, or track this as a known plugin limitation when reviewing CI failures.
