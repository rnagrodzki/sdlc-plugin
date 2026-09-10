# ship Configuration Reference

This document describes the `ship` section of `.sdlc-v2/local.json`, the persistent configuration `ship_prepare` merges against CLI flags. Settings here apply to every `ship` invocation in the repository unless overridden by a CLI flag, and — for the automation knobs — the separate `automation` section described below.

---

## File Location

```
<repo-root>/.sdlc-v2/local.json
```

Create it manually or run `/setup` to walk through an interactive setup. There is no `--init-config` walkthrough in this port (see "No init-config walkthrough" below).

---

## Config version auto-migrates — you never hand-edit it

`.sdlc-v2/config.json` carries a `schemaVersion` (current: `5`). Before `ship_prepare` does anything else, it runs an auto-migrate gate (`configmigrate.MigrateWithBackup`) that classifies the project's config into exactly one of three outcomes:

- **Missing** (no `config.json` and no legacy marker at all) — hard-fails with an actionable error pointing at `/setup`. There is nothing to migrate from.
- **Current** (`schemaVersion` already `5`) — no-op. No file is touched, no backup written, no change reported.
- **Stale** (legacy layout, or an older `schemaVersion`) — backs up the existing `config.json` to `config.json.bak`, then migrates it (and `local.json`, if also stale) up to schema version 5 in place. The applied migration steps are reported back to the caller.

Only one case still hard-fails after this gate: **too new** — a `schemaVersion` newer than this plugin build understands. That is a genuine version mismatch (upgrade the plugin), not something auto-migration can fix.

**Do not instruct a user to manually edit `schemaVersion` or hand-migrate `.sdlc-v2/config.json` for a version bump.** The gate already does this on every `ship_prepare` call — the only user-facing action ever needed is running `/setup` when no config exists at all, or upgrading the plugin when the config is too new.

---

## Full Example

```json
{
  "ship": {
    "steps": ["execute", "commit", "review", "verify-openspec", "archive-openspec", "pr", "verify-pipeline", "await-remote-review", "learnings-commit"],
    "quick": ["execute", "commit", "pr"],
    "bump": "patch",
    "draft": false,
    "auto": false,
    "reviewThreshold": "high",
    "rebase": "auto",
    "verifyPipelineTimeout": 1200,
    "verifyPipelineInterval": 60,
    "verifyPipelineMaxIterations": 3,
    "awaitRemoteReviewTimeout": 600,
    "awaitRemoteReviewInterval": 60,
    "awaitRemoteReviewers": ["copilot"],
    "executeWaveTimeout": 1800,
    "executeWaveInterval": 60,
    "execute": { "commitWaves": false }
  },
  "automation": {
    "mode": "supervised",
    "reviewFixIterations": 3,
    "reviewFixSeverityThreshold": "high",
    "steps": { "verify-pipeline": "auto" }
  }
}
```

`verify-pipeline` and `await-remote-review` are opt-in members of `ship.steps[]`. Add them only when you want post-PR CI verification or to await an automated reviewer's verdict. `verify-openspec` is an OpenSpec-gated opt-in — add it between `review` and `archive-openspec` when you want the pipeline to validate implementation completeness against the spec before archiving.

---

## Field Reference (`ship` section)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `steps` | `string[]` | `["execute","commit","review","archive-openspec","pr","learnings-commit"]` | Pipeline steps to run. Allowed values: `execute`, `commit`, `review`, `verify-openspec` (opt-in), `archive-openspec`, `pr`, `verify-pipeline` (opt-in), `await-remote-review` (opt-in), `learnings-commit`. `received-review` and `commit-fixes` are conditional sub-steps, not `steps[]` members — see the main skill. There is no standalone `version` step in this port — see `reference.md`'s Gotchas. |
| `quick` | `string[]` | unset | Shortened step list used when `ship_prepare` is called with `quick: true`. Unset means quick mode resolves to an empty step list — do not offer `--quick` on a project without a configured `quick` array. |
| `bump` | `"patch"` \| `"minor"` \| `"major"` \| pre-release label | `"patch"` | Default release bump, read at the main skill's step 6b and forwarded (as `releaseLevel`/`releasePreRelease`) to the `pr` step's `pr_apply` call — not applied by any standalone step. Overridden by an explicit `bump` on `ship_prepare`'s input. A configured `version.preRelease` label (a separate, top-level config section) overrides this default too, but never overrides an explicit CLI/tool-input bump. |
| `draft` | `boolean` | `false` | When `true`, PRs are created as drafts. |
| `auto` | `boolean` | `false` | Legacy pipeline-wide auto flag: when `true`, `ship_prepare` resolves `auto: true` and this pipeline suppresses its own confirmation prompts. Distinct from the `automation` section below — see "Two automation mechanisms." |
| `reviewThreshold` | `"critical"` \| `"high"` \| `"medium"` \| `"low"` | `"high"` | Minimum review-finding severity that triggers the received-review fix loop. See table below. |
| `rebase` | `boolean` \| `"auto"` \| `"skip"` \| any string | `"auto"` | A JSON `true`/`false` is coerced to `"auto"`/`"skip"`; any other string is passed through as-is. `"auto"` rebases onto the default branch after all commits, before the next configured main-loop step; `"skip"` never rebases. There is no `"prompt"` mode in this port — treat any unrecognized string as informational only, not as a request to ask the user. |
| `verifyPipelineTimeout` | `integer` (≥30) | `1200` | Maximum seconds `verify-pipeline` polls CI checks before giving up. |
| `verifyPipelineInterval` | `integer` (≥10) | `60` | Seconds between `verify-pipeline` poll probes. |
| `verifyPipelineMaxIterations` | `integer` (1–10) | `3` | Maximum analyze-fix-recheck iterations before `verify-pipeline` gives up. |
| `awaitRemoteReviewTimeout` | `integer` (≥30) | `600` | Maximum seconds `await-remote-review` polls for a reviewer response. |
| `awaitRemoteReviewInterval` | `integer` (≥10) | `60` | Seconds between `await-remote-review` poll probes. |
| `awaitRemoteReviewers` | `string[]` (minItems 1) | `["copilot"]` | Reviewer logins (case-insensitive) whose review satisfies the `await-remote-review` gate. |
| `executeWaveTimeout` | `integer` (60–3600) | `1800` | Maximum seconds a single execute wave may run. Forwarded to `execute`. The `3600` ceiling is `shipmeta.MaxWaveTimeoutSeconds`. |
| `executeWaveInterval` | `integer` (≥10) | `60` | Seconds between execute wave liveness poll attempts. Forwarded to `execute`. |
| `execute.commitWaves` | `boolean` (nested under `execute`) | `false` | Forwarded to `execute` as its per-wave commit behavior. A non-boolean value here does not error — `ship_prepare` records `commitWavesInvalidType: true` in its output instead; treat that as a warning to surface, not a hard failure. |

There is no `workspace` field in this port. `ship_prepare` reads no such config key, and this pipeline never creates a git worktree — `execute` is always isolated with a feature branch (see `reference.md`'s "No `workspace` config field, no worktree mode"). If a project's `.sdlc-v2/local.json` still carries `ship.workspace` from the source skill, it is silently ignored — do not treat its presence as an error, and do not attempt to honor a `"worktree"` value.

### reviewThreshold Levels

| Value | Which severities trigger the fix loop |
|-------|---------------------------------------|
| `"critical"` | Critical only |
| `"high"` | Critical + High |
| `"medium"` | Critical + High + Medium |
| `"low"` | Critical + High + Medium + Low (every finding) |

At `"high"` (the default), findings rated Medium or lower are reported but do not block the ship pipeline. `"low"` is the strictest setting — every finding, regardless of severity, triggers the fix loop.

### Legacy CLI sugar — not supported

The source skill's `--preset full|balanced|minimal` and `--skip <step,…>` flags have no field on `ShipPrepareIn`. There is nothing to expand or subtract — passing either concept to this port has no effect beyond whatever `steps` you resolve explicitly. Do not document a preset/skip combination flow here; it does not exist in this port.

---

## Merge Precedence

`ship_prepare` resolves `steps` in this order, recorded per-field in its `sources` output:

```
explicit steps input  >  quick (resolves ship.quick)  >  .sdlc-v2/local.json (ship.steps)  >  built-in defaults
```

`quick` and an explicit non-empty `steps` list are mutually exclusive in practice — when both are supplied, the explicit `steps` list wins (see `sources.steps` to confirm which tier actually applied). `auto`, `draft`, `bump`, `reviewThreshold`, `rebase`, and the numeric timing knobs each follow their own cli-input > config > default chain — see the Field Reference table above and `ship.go`'s `mergeShipFlags` for the authoritative per-field precedence.

---

## Two automation mechanisms — do not conflate

This port has **two independent knobs**, both of which end up controlling how much the pipeline pauses:

1. **`ship.auto`** (this document's `auto` field, legacy-shaped) — a single pipeline-wide boolean. When `true`, this skill suppresses its own confirmation prompts (the Step loop's dispatch confirmation, the archive-openspec consent gate, etc.) throughout the run.
2. **`automation` section** (new, separate top-level section of `.sdlc-v2/local.json` — sibling of `ship`, not nested under it) — a per-step automation policy read independently by `ship_state{action:"next"}`:
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

These do not automatically agree. `ship.auto: true` with no `automation` section still leaves every step resolving to `"confirm"` from `ship_state{action:"next"}`'s point of view (its default is `"supervised"`, independent of `ship.auto`). Treat `ship.auto`/an explicit `auto` tool input as the pipeline-wide prompt-suppression switch, and the `automation` section as the finer-grained per-step signal the KD14 executor loop reads — document both to a user configuring `.sdlc-v2/local.json`, and do not assume setting one sets the other.

There is no standalone version step in this port. Release-intent resolution — reading `flags.bump`/`sources.bump` and, under interactive mode, confirming or letting the user override it (the main skill's step 6b) — is fully covered by `automation.mode: unattended` (or `ship.auto: true`) like any other step, since it skips its own confirmation prompt in either case. The real version bump/tag/CHANGELOG write happens post-merge via CI (`release-on-main.yml`/`verify-release-intent.yml`/`promote-release.yml`), outside this pipeline's automation surface entirely — see `reference.md`.

---

## No `--init-config` walkthrough

The source skill's `--init-config` entry mode ran an 8-step interactive questionnaire (steps to run, bump type, draft, auto, workspace isolation, rebase strategy, review threshold) and wrote the `ship` section via a dedicated init script. This port has no equivalent tool or script for that walkthrough. `entry-modes.md`'s `--init-config` handler redirects unconditionally to `/setup` — read that file for the exact wording. Do not reconstruct the source questionnaire inline in this skill, even partially, and do not write `.sdlc-v2/local.json` directly from this skill's own logic.

---

## Team Configuration Examples

### Solo developer — move fast

Minimal step set, fully automated. There is no separate version step to skip in this port — release intent (default `bump: patch`) still resolves via the `pr` step's `pr_apply` call.

```json
{
  "ship": {
    "steps": ["execute", "commit", "review", "archive-openspec", "pr"],
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
    "steps": ["execute", "commit", "review", "archive-openspec", "pr"],
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
    "steps": ["execute", "commit", "review", "pr", "verify-pipeline"],
    "bump": "patch",
    "draft": false,
    "auto": false,
    "reviewThreshold": "medium",
    "verifyPipelineTimeout": 1800,
    "verifyPipelineMaxIterations": 5
  }
}
```
