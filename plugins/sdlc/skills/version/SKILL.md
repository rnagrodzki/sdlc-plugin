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

**Announce at start:** "I'm using version (sdlc v{sdlc_version})." — extract the version from the
`sdlc:` line in the session-start system-reminder. If no version is in context, omit the
parenthetical.

---

## Step 0 (DIAGNOSE): Call `version_prepare`

```
version_prepare({ skipConfigCheck: false, sessionID: "" }) → data
```

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
- Do not call `scaffold_ci` here. If the user asks about CI release automation, point them at
  running `scaffold_ci` themselves.

## Step 1 (PLAN): Determine Bump Level and Draft Release Notes

**Resolve the level** (`major`/`minor`/`patch`): use an explicit skill argument if one was given,
else `conventionalSummary.suggest`.

**Breaking-change gate:** if `conventionalSummary.breaking > 0` and the resolved level isn't
`major`, warn and recommend `major` instead.

**RC check:** look up `existingRCs[bumpOptions[level].result]`. If present, show the existing RC
tags and ask whether this is another release candidate or the final release. Choose RC when `--rc`
was passed, the existing-RCs answer says so, or the user asks for one — set `preRelease: "rc"` and
show `bumpOptions[level].rcNext` as the preview version. `pr_apply` recomputes the exact version
independently when the PR is opened, so the final tag may differ if the branch moved since.

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
