---
name: version-sdlc
description: "Use this skill when bumping a project version, creating a git release tag, generating a changelog, or performing a full semantic release workflow, updating an existing changelog entry for the current version, or retagging the current version at HEAD. Consumes pre-computed context from skill/version.js and handles the complete release process. Use --changelog without a bump type to update the changelog for the already-tagged current version. Use --retag to move an existing tag to HEAD. Arguments: [major|minor|patch|<label>] [--init] [--pre <label>] [--no-push] [--changelog] [--hotfix] [--retag] [--auto]. The positional `<label>` form (e.g. `version-sdlc rc`) is sugar for `--bump patch --pre <label>` and accepts any pre-release label matching `^[a-z][a-z0-9]*$`. Triggers on: version bump, create release, bump version, tag release, generate changelog, semantic versioning, semver bump, pre-release, release candidate, retag release. Use --auto to skip interactive approval prompts (release plan is still displayed)."
user-invocable: true
argument-hint: "[major|minor|patch|<label>] [--pre <label>] [--changelog] [--hotfix] [--retag] [--auto]"
model: haiku
---

# Versioning Releases Skill

Call the `version_prepare` MCP tool for release context, determine the bump and an optional
changelog entry, then execute the bump via `version_apply` and the release commit via
`commit_apply`.

**Announce at start:** "I'm using version-sdlc (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

## Port Notes (read before using this skill)

This is a Go/MCP port. Its tool surface (`version_prepare`, `version_apply`) supports only
the **Release** flow. The frontmatter's argument-hint describes a richer feature set that no
longer applies in full:

- **Init, Changelog-Update, and Retag flows are not currently available in this skill.**
  `version_prepare`'s output always reports `flow: "release"` — there is no flow dispatch
  left to do. `--init`, `--retag`, and a bare `--changelog` (without a bump) are all no-ops
  here. See `init-workflow.md` and `changelog-workflow.md` for what those flows used to do.
- There is no "tag mode." `version_prepare` requires one of a fixed set of version files
  (`package.json`, `plugin.json`, `Cargo.toml`, `pyproject.toml`, `pubspec.yaml`, or a
  `VERSION` file) — if none exists, the tool call fails outright.
- No `config` object and no `.sdlc/config.json` `version` section are read by this port: no
  `ticketPrefix`, no configured default pre-release label, no configured changelog default,
  no hotfix/no-push flags at the tool level. All of these become your own read of the skill's
  invocation arguments, described inline below.
- Pre-releases are numeric-only (`1.2.3` → `1.2.4-0` → `1.2.4-1`). There is no custom label
  train (`rc`/`beta`/`alpha` with independent counters) — `--pre <label>` cannot be honored as
  a named train in this port.
- There is no branch-guard field on `version_prepare`'s output at all (unlike commit-sdlc's
  permanently-stubbed one) — this port has nothing to check for an expected-branch gate.
- **This port supports the Release flow only.** Tag creation and pushing the release are not
  supported by this port; complete those steps yourself outside this skill. The release
  commit (Step 8) routes through `commit_apply`, which stages everything currently in the
  working tree — broader than a targeted add of just the version file and changelog.
- `version_prepare` does not check for uncommitted changes. Commit anything you want included
  before running this skill.

## Workflow

## Step 0 — Plan Mode Check

If the system context contains "Plan mode is active":

1. Announce: "This skill requires write operations (editing the version file and committing). Exit plan mode first, then re-invoke `/version-sdlc`."
2. Stop. Do not proceed to subsequent steps.

---

### Step 0 (CONSUME): Call `version_prepare`

```
version_prepare({ skipConfigCheck: false, sessionID: "" }) → data
```

**On tool error:** show the error to the user and stop. (A missing/unsupported version file
surfaces here — there is no tag-mode fallback in this port.)

Treat the returned `data` as `VERSION_CONTEXT`.

**If `VERSION_CONTEXT.errors` is non-empty**, show each error message and stop.

**If `VERSION_CONTEXT.warnings` is non-empty**, show the warnings to the user before
continuing.

`VERSION_CONTEXT.flow` is always `"release"` in this port — proceed directly to the Release
Workflow below. There is no other branch to check for.

---

### Release Workflow

### Step 1 (CONSUME): Read the Context

| Field | Description |
| ----- | ----------- |
| `versionSource.path` / `.type` / `.version` | The detected version file and its current version |
| `bumpOptions[]` | `{ level, result, current }` entries for `major`, `minor`, `patch` only — pre-computed candidate next versions. There is no pre-computed `preRelease` entry; a `prerelease`/`premajor`/`preminor`/`prepatch` bump's resulting version is only known once `version_apply` returns it. |
| `tags.all` / `.atHead` / `.latest` / `.tagPrefix` | Existing tags, tags at HEAD, the latest tag, and the detected prefix (`"v"` when most existing tags use it, otherwise `""`) |
| `commitsSinceTag[]` | Commit subjects since the latest tag (or all commits if there is no tag yet) |
| `conventionalSummary.breaking` / `.feat` / `.fix` / `.other` / `.total` / `.suggest` | Counts by conventional-commit type and an auto-suggested bump level — **informational only**, never applied automatically |
| `changelogExists` | Whether `CHANGELOG.md` already exists |
| `idempotency.alreadyBumped` / `.tagAtHead` | Whether a semver tag already sits at HEAD, and which one |

### Step 2 (PLAN): Determine Bump Type and Draft Changelog Notes

**Determine the bump level**, in this order:

1. If the skill invocation included an explicit `major`, `minor`, or `patch` argument, use it.
2. If it included an explicit semver string, use that string verbatim as `level` (passed
   through to `version_apply` as-is).
3. Otherwise, use `conventionalSummary.suggest` as a recommendation and confirm it with the
   user at the Step 5 approval prompt rather than applying it silently.

For a pre-release, use `level: "prerelease"` (or `"premajor"`/`"preminor"`/`"prepatch"` for a
first pre-release off a bumped base) — remember this only produces a numeric suffix, not a
named label.

**Breaking-change gate:** if `conventionalSummary.breaking > 0` and the resolved bump is not
`major`, suggest `major` instead — unless the resolved level is one of the `pre*` levels
(pre-release trains skip this nag).

**Draft changelog notes (optional):** decide whether to include a changelog entry by reading
the skill's own invocation arguments (was `--changelog` passed?) or asking the user when
unclear; `changelogExists` is useful context (an existing file suggests the project wants
one). If including notes:

- Use Keep a Changelog style bullets — do **not** write the `## [version] - date` heading
  yourself; `version_apply` generates that heading automatically. Pass only the body (e.g.
  `### Added\n- ...`) as `notes`.
- Map commit types to sections: `feat` → **Added**, `fix` → **Fixed**, `refactor`/`perf` →
  **Changed**; skip `chore`/`docs`/`test`/`ci`/`build`/`style` unless clearly user-facing.
- Rewrite unclear or implementation-focused commit subjects into user-facing language. Never
  fabricate an entry not backed by a real commit in `commitsSinceTag`.
- This port has no `ticketPrefix` config, so it does not append ticket-ID references to
  changelog entries — add them yourself if you already know them from conversation context.

### Step 2.5 (BRANCH-GUARD): not available in this port

`version_prepare`'s output has no branch-guard field at all — skip straight to Step 2.6.

### Step 2.6 (IDEMPOTENCY): Already-Bumped Check

If `idempotency.alreadyBumped` is `true`:

- Do not call `version_apply` or `commit_apply`.
- Report the skip: `status: skipped — HEAD already carries release tag <idempotency.tagAtHead>`.
- Halt the workflow here.

Otherwise, proceed to Step 3.

### Step 3 (CRITIQUE) / Step 4 (IMPROVE)

Review the planned version and changelog draft against the Quality Gates table below. Fix
anything that fails before proceeding (max 2 iterations per gate).

### Step 5 (DO): Present Release Plan for Approval

**Auto mode:** if `--auto` was passed to this skill invocation, skip the AskUserQuestion
prompt entirely. Still display the full release plan for visibility, then proceed directly to
Step 6. Treat the response as an implicit `yes`. (This is your own reading of the invocation
arguments — `version_prepare`'s output carries no `flags.auto` field in this port.)

```
Release Plan
────────────────────────────────────────────
Version:      {versionSource.version} → {newVersion}
Version file: {versionSource.path}
Changelog:    yes / no
────────────────────────────────────────────
This bumps the version, optionally updates the changelog, and creates a release commit.
Creating the tag and pushing are not supported by this port — do those yourself afterward.
```

If including changelog notes, show the draft entry between the plan and the prompt.

Use AskUserQuestion to ask:
> Execute this release?

Options: **yes** — execute | **edit** — describe what to change | **cancel** — abort

On `edit`: ask what to change, revise, and present again. Loop until explicit `yes` or
`cancel`.

### Step 6 (CRITIQUE post-execution plan): Verify Pre-conditions

This port has no `conflictsWithNext` field and no remote-state field, so most of the original
pre-flight checks have no data to run against. What remains:

- Optionally cross-check your intended tag name (`tags.tagPrefix + newVersion`) against
  `tags.all` yourself — there is no dedicated conflict flag for this in this port.
- If `commit_apply` later fails because git identity (name/email) is not configured, surface
  that error directly; there is no separate pre-check for it here.

### Step 7 (IMPROVE)

Resolve anything found in Step 6. If a blocking issue cannot be resolved, report it and stop.

### Step 7.5 (CHECK): CI Scripts Up To Date

```
scaffold_ci({ force: false }) → { warnings: [...], files: [{ path, action, installedVersion, currentVersion, group }] }
```

Unlike the original script, `force: false` is not a pure dry run: it writes any file that is
genuinely missing, and only warns (without writing) about files whose `action` is
`"outdated"`. There is no check-only mode and no `--changelog`-conditional grouping in this
port — all four manifest files are always processed together.

If any `warnings` mention outdated files, show them and ask the user whether to update:
> Update CI scripts? (yes / no) — this does not block the release.

On `yes`: call `scaffold_ci({ force: true })` to overwrite the outdated files. On `no`:
continue with the release regardless — this step never blocks it.

### Step 8 (EXECUTE): Execute the Release

Only execute after explicit `yes` from Step 5, or implicit approval in auto mode.

1. **Link verification — HARD GATE.** Skip this sub-step if you are not including changelog
   notes. Otherwise, write the drafted `notes` body to a scratch file (via the Write tool),
   then:

   ```
   links_validate({ file: "<scratch file path>", offline: false }) → { results: [...] }
   ```

   Any `results[]` entry whose `status` is `"violation"` is a hard-gate failure (`"ok"` and
   `"skipped"` are both fine). On any `"violation"` entry, do NOT proceed. Surface the full
   violation list (`url`, `line`, `reason`, `detail`) to the user and stop. Do not retry. Do
   not edit URLs without user input.

2. **Bump the version:**

   ```
   version_apply({ level: <resolved level or explicit semver>, notes: <changelog body or "">, skipConfigCheck: false, sessionID: "" })
     → { previousVersion, newVersion, versionFile, changelogFile, changed }
   ```

   **On tool error:** show the error and stop. If `changed` is `false`, the version was
   already at the target value — report that nothing changed and stop before committing.

3. **Compute the release tag name** (for the commit message only — this port does not create
   the tag): `NEW_TAG = tags.tagPrefix + newVersion`. If `tags.tagPrefix` is empty and no
   existing tags use a prefix, a leading `v` is a reasonable default for a first release —
   confirm with the user if you're unsure which convention this project wants.

4. **Commit the release:**

   ```
   commit_apply({ message: "chore(release): " + NEW_TAG, skipConfigCheck: false, sessionID: "" }) → { sha }
   ```

   **On tool error:** show the error and stop. Remember this stages the entire working tree,
   not just `versionFile`/`changelogFile` — see Port Notes.

Display the result:

```
✓ Release {newVersion} committed as {sha[:7]}.
  Version file: {versionFile}
  Changelog:    {changelogFile, or "not updated"}

Tag creation and pushing are not supported by this port — create the tag and push it
yourself.
```

---

## Quality Gates

| Gate | Check | Pass Criteria |
| ---- | ----- | ------------- |
| Semver correctness | New version is valid semver | `major.minor.patch[-pre]`, no leading zeros |
| Breaking change bump | If `conventionalSummary.breaking > 0`, bump is major (or a `pre*` level) | Warn if minor/patch chosen with breaking commits |
| Changelog completeness | All user-facing commits are represented | No feat/fix commits silently omitted (when notes are included) |
| No fabricated entries | Every changelog entry traces to a real commit in `commitsSinceTag` | — |
| Commits exist | There are commits to release, or the bump is a pre-release | `commitsSinceTag.length > 0` OR a `pre*` level |

## Best Practices

1. Always show the full release plan before calling `version_apply` or `commit_apply`
2. Use `conventionalSummary` to sanity-check the requested bump, but let the user's explicit
   argument win
3. Changelog entries should be user-facing and outcome-focused, not implementation-focused
4. Running a plain `major`/`minor`/`patch` bump after pre-releases naturally "graduates" past
   them — this port has no separate graduation concept, it is just a normal bump

## DO NOT

- Call `version_apply` or `commit_apply` without explicit user approval (unless `--auto`)
- Fabricate commit descriptions or changelog entries not backed by real commits
- Skip the critique step (Steps 3–4) even if the plan looks obviously correct
- Write the changelog heading yourself — `version_apply` generates it; pass only the body
- Claim this port creates a tag or pushes anything — it does neither

## Error Recovery

| Error | Recovery | Invoke error-report-sdlc? |
| ----- | -------- | ------------------------- |
| `version_prepare` tool error (no supported version file) | Show the error, stop | No — user/project setup issue |
| `version_apply` tool error | Show the error, stop | Yes, if it looks like a defect |
| `version_apply` returns `changed: false` | Report no-op, stop before committing | No |
| `commit_apply` tool error | Show the error, stop | Yes, if it looks like a defect |
| `links_validate` reports violations | Show the violation list, stop, do not retry automatically | No — user needs to fix the links |

When invoking `error-report-sdlc`, provide:
- **Skill**: version-sdlc
- **Step**: the step where the tool call failed
- **Operation**: the MCP tool name and input
- **Error**: the tool's returned error message
- **Suggested investigation**: check the version file format; verify git identity is configured

---

## Gotchas

- **No tag mode**: if the project has none of `package.json`, `plugin.json`, `Cargo.toml`,
  `pyproject.toml`, `pubspec.yaml`, or a `VERSION` file, `version_prepare` fails outright.
- **No custom pre-release labels**: `--pre rc` cannot start or continue a named `rc` train in
  this port — only the generic numeric `prerelease` level is available.
- **Idempotent version_apply**: calling it again with the same resolved version is a no-op
  (`changed: false`) — this is not an error, just nothing to commit.
- **`commit_apply` stages everything**: if the working tree has unrelated changes at execute
  time, they will be included in the release commit. Commit or discard them first if that
  matters.
- **No automatic ticket-ID annotation**: without a `ticketPrefix` config surface, this port
  never auto-appends ticket references to changelog entries.
- **Tag and push are manual**: nothing in this port creates the git tag or pushes it — treat
  this skill's job as done once the release commit exists.

## Changelog Accuracy and Limitations

The drafted changelog is a **draft, not a source of truth**. Correctness is the developer's
responsibility. In addition to the general limitations of LLM-drafted content (misinterpreted
scope, missed nuance), this port specifically has no CI-scaffolded changelog-enforcement
workflow beyond what `scaffold_ci` installs, and no dedicated changelog-update flow to correct
an entry after the fact (see `changelog-workflow.md`) — review the entry yourself before
treating it as final.

## Learning Capture

After completing a release or encountering unexpected behavior, append to
`.sdlc/learnings/log.md`:

```
## YYYY-MM-DD — version-sdlc: <brief summary>
<what happened, what was learned>
```

## What's Next

After completing the release, common follow-ups include:
- Creating and pushing the release tag yourself
- `/jira-sdlc` — update Jira ticket status

## See Also

- [`/commit-sdlc`](../commit-sdlc/SKILL.md) — commit changes before tagging a release
- [`/jira-sdlc`](../jira-sdlc/SKILL.md) — update Jira ticket status after release
- [`/pr-sdlc`](../pr-sdlc/SKILL.md) — the PR that triggered this release
- `init-workflow.md` — describes the original init flow, not available in this port
- `changelog-workflow.md` — describes the original changelog-update flow, not available in this port
