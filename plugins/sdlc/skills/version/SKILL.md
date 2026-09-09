---
name: version
description: "Use this skill to diagnose release readiness and draft a release plan for a project version: a resolved bump level, an optional release-candidate train, and drafted release notes. Consumes the `version_prepare` MCP tool for diagnostics; its output is handed to the `pr` skill's `pr_apply` call. This skill never bumps the version file, creates a tag, writes a changelog, or commits anything. Triggers on: version bump, plan a release, draft release notes, release candidate, rc release, semver bump, bump version. Arguments: [major|minor|patch] [--rc] [--auto]."
user-invocable: true
argument-hint: "[major|minor|patch] [--rc] [--auto]"
model: haiku
---

# Versioning Releases Skill

Call `version_prepare` to diagnose release readiness, then draft a bump level and release notes for
the `pr` skill to carry through `pr_apply`. This skill is diagnose-and-plan only: it never calls
`version_apply` or `commit_apply`, never creates a tag, and never writes the version file or a
changelog.

**Versioning modes:** Projects define their versioning strategy in `.sdlc-v2/config.json` via a `version` section:
- **File mode** (`mode: "file"` or omitted): Current version is read from a version file (e.g., `package.json`,
  `VERSION`, or `Cargo.toml`). This is the default and covers most projects. `versionSource.type` is `"file"`.
- **Tag mode** (`mode: "tag"`): Current version is derived from the highest semver git tag instead of a file.
  Useful for tag-only projects that don't maintain a version file. `versionSource.type` is `"tag"` and `path` is
  empty. If no semver tags exist, defaults to `"0.0.0"` with a warning.

**Announce at start:** "I'm using version (sdlc v{sdlc_version})." — extract the version from the
`sdlc:` line in the session-start system-reminder. If no version is in context, omit the
parenthetical.

---

## Step 0 (DIAGNOSE): Call `version_prepare`

```
version_prepare({ skipConfigCheck: false, sessionID: "" }) → data
```

**Version source detection:** `data.versionSource` describes how the current version was determined:
- `type: "file"` (file mode): `path` is the detected or configured version file (e.g., `package.json`),
  `version` is the parsed version string.
- `type: "tag"` (tag mode): `path` is empty, `version` is derived from the highest semver git tag.

**Continue with diagnostics:**
- `data.errors` non-empty → show each message, stop.
- `data.warnings` non-empty → show them, continue.
- `!data.configPresent && data.proposedConfig` → show the proposed `version` config section and
  offer to write it: `setup_write_sections({ sectionsJson: JSON.stringify({ version:
  data.proposedConfig }) })`. On approval, write it and re-call `version_prepare`. On decline,
  continue — `version_prepare` still auto-detects the version file without a config section.
- `data.hasDirtyFiles` → warn: "N uncommitted file(s)" (list `dirtyFiles`) — informational only,
  does not block planning.
- `data.idempotency.alreadyBumped` → report `status: skipped — HEAD already carries release tag
  <idempotency.tagAtHead>` and stop.
- `data.versionDivergence` → show `versionDivergence.message` and suggest `git fetch && git rebase
  origin/<defaultBranch>` before planning further — the bump is computed off the higher of
  file/tag version, not the file alone.
- **CI release scaffolding:**
  - **File mode:** do not call `scaffold_ci` here. If the user asks about CI release automation,
    point them at running `scaffold_ci` themselves.
  - **Tag mode** (`data.versionConfig.mode == "tag"`): Glob `.github/workflows/release-on-main.yml`
    and `.github/workflows/retag-release.yml`. If **neither** exists, offer via AskUserQuestion:

    > Project uses tag-only versioning — no version file to bump locally.
    > CI release workflows handle tag creation and version bumping post-merge.
    > Scaffold release CI now?
    > 1. **Yes** — run `scaffold_ci` to add release-on-main + retag-release workflows
    > 2. **Skip** — continue without CI scaffolding

    On **yes**: call `scaffold_ci({ force: false })` — `scaffold_ci`'s only input is `force`
    (`false` creates missing files without touching any unrelated manifest entry that already
    exists) — then continue to Step 1. On **skip**, continue to Step 1. If either workflow already
    exists, skip the offer entirely and continue to Step 1.

## Step 1 (PLAN): Determine Bump Level and Draft Release Notes

**Resolve the level** (`major`/`minor`/`patch`): use an explicit skill argument if one was given,
else `conventionalSummary.suggest`.

**Breaking-change gate:** if `conventionalSummary.breaking > 0` and the resolved level isn't
`major`, warn and recommend `major` instead.

**RC check:** look up `existingRCs[bumpOptions[level].result]`. If present, show the existing RC
tags. Default answer is `bumpOptions[level].suggestedPreRelease` — `"rc"` when the version config's
`rcAutoContinue` is true (the default: once a version has an RC out, stay in RC mode until told
otherwise), empty when it's false. Under `--auto`, take this default without asking. Otherwise ask
whether this is another release candidate or the final release, showing the default as the
recommended choice. Choose RC when `--rc` was passed, the (possibly defaulted) answer says so, or
the user asks for one — set `preRelease: "rc"` and show `bumpOptions[level].rcNext` as the preview
version. `pr_apply` recomputes the exact version independently when the PR is opened, so the final
tag may differ if the branch moved since.

**Draft release notes** from `commitsSinceTag`:
- Keep a Changelog-style bullets. Do **not** write a `## [version]` heading — `pr_apply` generates
  that automatically; pass only the body.
- Map `feat` → **Added**, `fix` → **Fixed**, `refactor`/`perf` → **Changed**; skip
  `chore`/`docs`/`test`/`ci`/`build`/`style` unless clearly user-facing.
- Rewrite unclear or implementation-focused commit subjects into user-facing language. Never
  fabricate an entry not backed by a real commit in `commitsSinceTag`.

**One approval prompt.** Show the plan:

```
Release Plan
────────────────────────────────────────────
Level:    {level}{preRelease and " (" + preRelease + ")"}
Preview:  {bumpOptions[level].result or .rcNext}
Notes:    <drafted body, or "none">
────────────────────────────────────────────
This does not bump, tag, or commit anything — it hands level/notes/preRelease to the pr skill.
```

Ask via AskUserQuestion: **yes** — use this plan | **edit** — describe what to change | **cancel**
— abort. On `edit`, revise and present again; loop until `yes`/`cancel`. Under `--auto`, skip the
prompt but still display the plan (treat as implicit `yes`).

**Output:** `level`, `notes`, and `preRelease` (only when an RC was chosen). Pass these to `/pr`
(or `pr_apply` directly) as `releaseLevel`, `releaseNotes`, and `releasePreRelease`.

---

## Quality Gates

| Gate | Check | Pass Criteria |
| ---- | ----- | -------------- |
| Semver correctness | Resolved level is `major`, `minor`, or `patch` | Matches a `bumpOptions[]` entry |
| Breaking change bump | If `conventionalSummary.breaking > 0`, level is `major` | Warn if `minor`/`patch` chosen with breaking commits |
| Notes completeness | User-facing commits are represented | No `feat`/`fix` commits silently omitted (when notes are drafted) |
| No fabricated entries | Every note traces to a real commit in `commitsSinceTag` | — |
| Commits exist | There are commits to release, or this continues an RC train | `commitsSinceTag.length > 0` OR `preRelease: "rc"` |

## See Also

- [`/pr`](../pr/SKILL.md) — carries `releaseLevel`/`releaseNotes`/`releasePreRelease` into
  `pr_apply`
- [`/jira`](../jira/SKILL.md) — update Jira ticket status after release
