# ship Configuration Reference

This document describes the `ship` section of `.sdlc-v2/local.toml`, the persistent configuration `ship_prepare` merges against CLI flags. Settings here apply to every `ship` invocation in the repository unless overridden by a CLI flag, and — for the automation knobs — the separate `automation` section described below.

---

## File Location

```
<repo-root>/.sdlc-v2/local.toml
```

Values in `~/.sdlc/local.toml` (or the path in `SDLC_USER_CONFIG`) apply when the project file does not set the key. Tables merge key by key, and a list in the project file replaces the user list.

Create it manually or run `/setup` to walk through an interactive setup. There is no `--init-config` walkthrough in this port (see "No init-config walkthrough" below).

---

## Config version check — nothing is migrated, you never hand-edit it

Before `ship_prepare` does anything else, it runs a config-version check (`configmigrate.MigrateWithBackup`). Despite the name, it migrates nothing and writes no backup. The version is read from which files exist under `.sdlc-v2/`, not from a `schemaVersion` field:

- **Missing** (no `config.toml`, `config.json`, `local.toml` or `local.json`) — fails with one `errors` entry prefixed `config-version:` that points at `/setup`.
- **Stale** (a JSON-era `config.json` with no `config.toml`, or a `local.json` with no `local.toml`) — fails the same way: `config-version: ... TOML config required. Run /setup to initialize.` There is no automated JSON-to-TOML path.
- **Current** (the TOML files are present) — no-op.

On either failure `ship_prepare` creates no state; the skill prints the error verbatim and stops.

**Do not instruct a user to manually edit `schemaVersion` or hand-migrate `.sdlc-v2/config.toml`.** The only user-facing action ever needed is running `/setup`.

---

## Full Example

```json
{
  "ship": {
    "steps": { "execute": true, "commit": true, "review": true, "verify-openspec": true, "archive-openspec": true, "harden": true, "pr": true, "verify-pipeline": true, "await-remote-review": true, "learnings-commit": true },
    "quick": { "execute": true, "commit": true, "pr": true },
    "bump": "patch",
    "draft": false,
    "auto": false,
    "reviewThreshold": "info",
    "rebase": "auto",
    "verifyPipelineTimeout": 1200,
    "verifyPipelineInterval": 60,
    "verifyPipelineMaxIterations": 3,
    "awaitRemoteReviewTimeout": 600,
    "awaitRemoteReviewInterval": 60,
    "awaitRemoteReviewers": ["copilot"],
    "executeWaveTimeout": 1800,
    "executeWaveInterval": 60,
    "execute": { "commitWaves": true }
  },
  "automation": {
    "mode": "supervised",
    "reviewFixIterations": 3,
    "reviewFixSeverityThreshold": "high",
    "steps": { "verify-pipeline": "auto" }
  }
}
```

`verify-pipeline` and `await-remote-review` are opt-in: set them to `true` in `[ship.steps]`. Set them only when you want post-PR CI verification or to await an automated reviewer's verdict. `verify-openspec` is an OpenSpec-gated opt-in — set it to `true` when you want the pipeline to validate implementation completeness against the spec before archiving. `harden` is an opt-in too — set it to `true` when you want review findings turned into guardrail/dimension edits committed before the PR. The plugin runs `harden` after `archive-openspec`. With it configured, `received-review` gets `--no-harden` so hardening runs once.

---

## Field Reference (`ship` section)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `steps` | table of step → boolean | on: `execute`, `commit`, `review`, `archive-openspec`, `pr`, `learnings-commit` | On/off flag per pipeline step. The plugin fixes the order. Allowed keys: `execute`, `commit`, `review`, `verify-openspec` (opt-in), `archive-openspec`, `harden` (opt-in — clusters review findings after rebase, invokes `/harden` on each, and commits its edits as a separate commit before `pr`. `/harden` has six surfaces: `plan-guardrails`, `execute-guardrails`, `review-dimensions`, `copilot-instructions`, `error-report-skill`, `skill-recommendation`. Only the first four are edited, so the commit covers the project's `config.toml`, its `review-dimensions/` directory and `.github/instructions/` (the three harden paths the main skill's `harden` step lists); `error-report-skill` and `skill-recommendation` are read-only context for the orchestrator), `pr`, `verify-pipeline` (opt-in), `await-remote-review` (opt-in), `learnings-commit`. A step not listed keeps its default. `received-review` and `commit-fixes` are conditional sub-steps, not `steps` keys — setting either one to `true` makes `ship_prepare` return an error; see the main skill. There is no standalone `version` step in this port — see `reference.md`'s Gotchas. |
| `quick` | table of step → boolean | unset | Steps for `quick: true`. A step not listed is off. Without the table, quick mode has no steps — do not offer `--quick` on a project without a configured `quick` table. |
| `bump` | `"patch"` \| `"minor"` \| `"major"` \| pre-release label | `"patch"` | Default release bump, read at the main skill's step 6b and forwarded (as `releaseLevel`/`releasePreRelease`) to the `pr` step's `pr_apply` call — not applied by any standalone step. Overridden by an explicit `bump` on `ship_prepare`'s input. A configured `version.preRelease` label (a separate, top-level config section) overrides this default too, but never overrides an explicit CLI/tool-input bump. |
| `draft` | `boolean` | `false` | When `true`, PRs are created as drafts. |
| `auto` | `boolean` | `false` | Legacy pipeline-wide auto flag: when `true`, `ship_prepare` resolves `auto: true` and this pipeline suppresses its own confirmation prompts. Distinct from the `automation` section below — see "Two automation mechanisms." |
| `reviewThreshold` | `"critical"` \| `"high"` \| `"medium"` \| `"low"` \| `"info"` | `"info"` | Minimum review-finding severity that triggers the received-review fix loop. The default `"info"` sends every finding, Info included, into the fix loop. `"low"` keeps the old behavior: Info findings are deferred (reason `below-threshold`). Any other value makes `ship_prepare` return an error. See table below. |
| `rebase` | `boolean` \| `"auto"` \| `"skip"` \| any string | `"auto"` | A JSON `true`/`false` is coerced to `"auto"`/`"skip"`; any other string is passed through as-is. `"auto"` rebases onto the default branch after the feature and review-fix commits, before the next configured main-loop step (the `harden` commit, when configured, lands after it); `"skip"` never rebases. There is no `"prompt"` mode in this port — treat any unrecognized string as informational only, not as a request to ask the user. |
| `verifyPipelineTimeout` | `integer` (≥30) | `1200` | Maximum seconds `verify-pipeline` polls CI checks before giving up. |
| `verifyPipelineInterval` | `integer` (≥10) | `60` | Seconds between `verify-pipeline` poll probes. |
| `verifyPipelineMaxIterations` | `integer` (1–10) | `3` | Maximum analyze-fix-recheck iterations before `verify-pipeline` gives up. |
| `awaitRemoteReviewTimeout` | `integer` (≥30) | `600` | Maximum seconds `await-remote-review` polls for a reviewer response. |
| `awaitRemoteReviewInterval` | `integer` (≥10) | `60` | Seconds between `await-remote-review` poll probes. |
| `awaitRemoteReviewers` | `string[]` (minItems 1) | `["copilot"]` | Reviewer logins (case-insensitive) whose review satisfies the `await-remote-review` gate. |
| `executeWaveTimeout` | `integer` (60–3600) | `1800` | Maximum seconds a single execute wave may run; also used as the per-task total-runtime threshold for stall classification. Forwarded to `execute`. The `3600` ceiling is `shipmeta.MaxWaveTimeoutSeconds`. |
| `executeWaveInterval` | `integer` (≥10) | `60` | Seconds between execute wave liveness poll attempts. Forwarded to `execute`. |
| `execute.commitWaves` | `boolean` (nested under `execute`) | `true` | Forwarded to `execute` as `--commit-waves`, but only when this key is explicitly set — an unset key leaves `execute`'s own top-level `execute.commitWaves` config (or its built-in default `true`) in charge. A non-boolean value here does not error — `ship_prepare` records `commitWavesInvalidType: true` in its output instead; treat that as a warning to surface, not a hard failure. |

There is no `workspace` field in this port. `ship_prepare` reads no such config key, and this pipeline never creates a git worktree — `execute` is always isolated with a feature branch (see `reference.md`'s "No `workspace` config field, no worktree mode"). If a project's `.sdlc-v2/local.toml` still carries `ship.workspace` from the source skill, it is silently ignored — do not treat its presence as an error, and do not attempt to honor a `"worktree"` value.

### reviewThreshold Levels

| Value | Which severities trigger the fix loop |
|-------|---------------------------------------|
| `"critical"` | Critical only |
| `"high"` | Critical + High |
| `"medium"` | Critical + High + Medium |
| `"low"` | Critical + High + Medium + Low |
| `"info"` | Critical + High + Medium + Low + Info (every finding) |

At `"info"` (the default), every finding enters the `received-review` fix loop. At `"low"`, Info findings stay below the threshold. Findings below the threshold are not dropped: `ship` records each one through `ship_state` `defer` (reason `below-threshold`) in `<MAIN_ROOT>/.sdlc-v2/history/deferred.json`, and Step 10 points to `/sdlc:deferred` for acting on them. That write is best-effort — when the `defer` narration carries `WARNING: could not persist`, retry with `ship_state` `deferred_add` or name the finding as UNACCOUNTED in the summary.

### Legacy CLI sugar — not supported

The source skill's `--preset full|balanced|minimal` and `--skip <step,…>` flags have no field on `ShipPrepareIn`. There is nothing to expand or subtract — passing either concept to this port has no effect beyond whatever `steps` you resolve explicitly. Do not document a preset/skip combination flow here; it does not exist in this port.

---

## Merge Precedence

`ship_prepare` resolves `steps` in this order:

```
explicit --steps (a set)  >  --quick ([ship.quick] table)  >  [ship.steps] table  >  built-in defaults
```

`[ship.steps]` and `[ship.quick]` merge per key: `~/.sdlc/local.toml` first, then `.sdlc-v2/local.toml` wins for each key it sets.

The plugin fixes the order: `execute`, `commit`, `review`, `verify-openspec`, `archive-openspec`, `harden`, `pr`, `verify-pipeline`, `await-remote-review`, `learnings-commit`.

`sources.steps` records the tier that applied as one of four values: `cli`, `quick`, `config`, or `default`. `config` covers both local.toml files: `config.ReadSection` merges `~/.sdlc/local.toml` and `.sdlc-v2/local.toml` (project wins per key) before `mergeShipFlags` sees the value, so `sources` cannot tell which of the two files supplied it.

`quick` and an explicit non-empty `steps` list are mutually exclusive in practice — when both are supplied, the explicit `steps` list wins (see `sources.steps` to confirm which tier actually applied). `auto`, `draft`, `bump`, `reviewThreshold`, `rebase`, and the numeric timing knobs each follow their own cli-input > config > default chain — see the Field Reference table above and `ship.go`'s `mergeShipFlags` for the authoritative per-field precedence.

---

## Two automation mechanisms — do not conflate

This port has **two independent knobs**, both of which end up controlling how much the pipeline pauses:

1. **`ship.auto`** (this document's `auto` field, legacy-shaped) — a single pipeline-wide boolean. When `true`, this skill suppresses its own confirmation prompts (the Step loop's dispatch confirmation, the archive-openspec consent gate, etc.) throughout the run — except the in-flight-run prompt at SKILL.md's Step loop item 2, which guards a resumable run that `ship_prepare` would delete, and the manual-push pause.
2. **`automation` section** (new, separate top-level section of `.sdlc-v2/local.toml` — sibling of `ship`, not nested under it) — a per-step automation policy read independently by `ship_state{action:"next"}`:
   ```json
   {
     "automation": {
       "mode": "supervised",
       "reviewFixIterations": 3,
       "reviewFixSeverityThreshold": "high",
       "steps": { "verify-pipeline": "auto" }
     }
   }
   ```
   - `mode`: `"supervised"` (default — unlisted steps resolve to `"confirm"`) or `"unattended"` (unlisted steps resolve to `"auto"`).
   - `steps`: per-step overrides (`"auto"` or `"confirm"`) that win over `mode` for that step name.
   - `reviewFixIterations` / `reviewFixSeverityThreshold`: bound the received-review fix loop independently of `ship.reviewThreshold`.

   `ship_state{action:"next"}`'s `automation` output field is this section's `StepMode(step)` result for whichever scaffolded step is next, defaulting to `"confirm"` on any config-read failure or when the section is absent entirely.

These do not automatically agree. `ship.auto: true` with no `automation` section still leaves every step resolving to `"confirm"` from `ship_state{action:"next"}`'s point of view (its default is `"supervised"`, independent of `ship.auto`). Treat `ship.auto`/an explicit `auto` tool input as the pipeline-wide prompt-suppression switch, and the `automation` section as the finer-grained per-step signal the KD14 executor loop reads — document both to a user configuring `.sdlc-v2/local.toml`, and do not assume setting one sets the other.

There is no standalone version step in this port. Release-intent resolution — reading `flags.bump`/`sources.bump` and, under interactive mode, confirming or letting the user override it (the main skill's step 6b) — is fully covered by `automation.mode: unattended` (or `ship.auto: true`) like any other step, since it skips its own confirmation prompt in either case. The real version bump/tag/CHANGELOG write happens post-merge via CI (`release-on-main.yml`/`verify-release-intent.yml`/`promote-release.yml`), outside this pipeline's automation surface entirely — see `reference.md`.

---

## No `--init-config` walkthrough

The source skill's `--init-config` entry mode ran an 8-step interactive questionnaire (steps to run, bump type, draft, auto, workspace isolation, rebase strategy, review threshold) and wrote the `ship` section via a dedicated init script. This port has no equivalent tool or script for that walkthrough. `entry-modes.md`'s `--init-config` handler redirects unconditionally to `/setup` — read that file for the exact wording. Do not reconstruct the source questionnaire inline in this skill, even partially, and do not write `.sdlc-v2/local.toml` directly from this skill's own logic.

---

## Team Configuration Examples

### Solo developer — move fast

Minimal step set, fully automated. There is no separate version step to skip in this port — release intent (default `bump: patch`) still resolves via the `pr` step's `pr_apply` call.

```json
{
  "ship": {
    "steps": { "execute": true, "commit": true, "review": true, "archive-openspec": true, "pr": true, "learnings-commit": false },
    "bump": "patch",
    "draft": false,
    "auto": true,
    "reviewThreshold": "critical"
  }
}
```

### Team with guardrails

All canonical steps run; review threshold catches high-severity findings; PRs default to draft.

```json
{
  "ship": {
    "steps": { "execute": true, "commit": true, "review": true, "archive-openspec": true, "pr": true, "learnings-commit": false },
    "bump": "minor",
    "draft": true,
    "auto": false,
    "reviewThreshold": "high"
  },
  "automation": {
    "mode": "supervised"
  }
}
```

### CI-adjacent — maximum confidence

Smallest step set with widest review threshold, plus post-PR CI verification. Suitable for regulated environments or release branches where medium-severity findings must be resolved before merging.

```json
{
  "ship": {
    "steps": { "execute": true, "commit": true, "review": true, "archive-openspec": false, "pr": true, "verify-pipeline": true, "learnings-commit": false },
    "bump": "patch",
    "draft": false,
    "auto": false,
    "reviewThreshold": "medium",
    "verifyPipelineTimeout": 1800,
    "verifyPipelineMaxIterations": 5
  }
}
```
