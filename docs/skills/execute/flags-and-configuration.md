# Flags and configuration

Read this page when you want to change how a `/sdlc:execute` run starts, how often it asks, or which models run the tasks. Only the plan file path is required.

## Example

```text
/sdlc:execute --plan tasks/migrate-db.md --quality full --auto
```

Runs the fastest quality tier without stopping for confirmation.

## Syntax

```text
/sdlc:execute <plan-file-path> [options]
/sdlc:execute --plan <path> [options]
/sdlc:execute --resume [--plan <path>]
```

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `<plan-file-path>` | Path to the plan file. Required unless you resume. | none |
| `--plan <path>` | Another way to give the plan file path. | none |
| `--quality <level>` | Quality tier: `full`, `balanced`, or `minimal`. See [Quality tiers](#quality-tiers). | A prompt. When `executePrefs.quality` is set, that value fixes the tier and no prompt shows. See [Configuration](#configuration). |
| `--resume` | Resume from the saved progress after an interruption. | off |
| `--rebase <mode>` | Rebase onto the base branch before the run: `auto`, `skip`, or `prompt`. `auto` rebases when the branch is behind and continues on the current base after a conflict. `prompt` asks first. | `skip` |
| `--auto` | Skip the interactive prompts. | off. `executePrefs.auto`, or a `/sdlc:ship` run in auto mode that started this skill, also turns auto mode on. Any one of the three sources is enough. See [Configuration](#configuration). |
| `--branch <name>` | Internal flag for a pipeline caller. The skill takes the caller's branch as given and skips its own branch detection. A standalone run never passes it. | not set: the skill detects the branch |
| `--wave-timeout <s>` | Max seconds a single wave can run. Also the total-runtime ceiling of each task when the server classifies the tasks that are still open. | `1800` |
| `--commit-waves <true\|false>` | Commit each wave separately. `/sdlc:ship` forwards this flag only when `ship.execute.commitWaves` is set. | `execute.commitWaves` in `config.toml`, else `true` |
| `--wave-interval <s>` | Seconds between the liveness polls of a wave. A worker with no heartbeat for 10x this value (default 600s) counts as stalled. A worker that never answers the reclaim fails after max(5x this value, 300s) (default 300s). See [Execute wave supervision](../../internal/execute-wave-supervision.md). | `60` |
| `--no-instructions` | Do not run the follow-up instructions of the skill. Ship passes this flag, because ship runs the lists itself. See [Follow-up instructions](#follow-up-instructions). | off |

### Quality tiers

| Value | What it does |
|-------|-------------|
| `full` | Fastest and cheapest. Uses lighter models. Skips spec compliance review. |
| `balanced` | Default. Good balance of speed and correctness. |
| `minimal` | Slowest but highest correctness. Uses the strongest models throughout. |

The tier prompt shows all 3 tiers with the model of each task. Your pick is the approval: the run starts at once. The prompt also offers `custom`, which lets you edit the model of each task, and `cancel`.

## Configuration

`.sdlc-v2/local.toml` (personal, gitignored), section `[executePrefs]`:

| Key | Type | Default | Effect |
|---|---|---|---|
| `executePrefs.auto` | boolean | `false` | Run unattended. Suppresses the branch, tier, resume and high-risk prompts. Never overrides an error-severity guardrail failure. |
| `executePrefs.quality` | `"full"` \| `"balanced"` \| `"minimal"` | unset | Fixes the model preset and skips the tier prompt. `--quality` overrides it. Unset plus `auto = true` resolves to `balanced`. A value outside the three tiers, from this key or from `--quality`, is not fatal: the skill prints a warning and uses the next source. |
| `executePrefs.highRiskAutoApprove` | boolean | `false` | A wave with a High-risk task proceeds without a second approval. The skill reads this key alone: it consults no plan-approval state. When a high-risk wave proceeds because of this key or `executePrefs.auto`, and not because of a per-run `--auto`, the skill prints a warning at wave start that names the config source. |

Skip the tier prompt on every run:

```toml
[executePrefs]
quality = "balanced"
```

**Resolution order.** The `--quality` or `--auto` flag comes first, then the auto mode of a `/sdlc:ship` run that started this skill, then these keys, then the built-in default. The `resolve-config` action of the `execute_state` tool does the merge. The skill prints each value with its source: `local` for these keys, and the label config for `execute.commitWaves`, because that value comes from `config.toml`.

**Project config.** `.sdlc-v2/config.toml` (project-level, committed) holds the `[execute]` keys:

| Key | Default | Effect |
|---|---|---|
| `execute.commitWaves` | `true` | Commit each wave separately. `--commit-waves` overrides it. |
| `execute.baseSync` | on | Set to `false` to turn off the [base sync between waves](./tips-and-gotchas.md#base-sync-between-waves). |
| `execute.guardrails` | empty | The execution guardrails of the project. See [Guardrails](./tips-and-gotchas.md#guardrails). |

**Old key location.** If `auto`, `quality` or `highRiskAutoApprove` is still under `[execute]` in `config.toml`, the skill moves it to `[executePrefs]` in `local.toml`, keeps all comments, and warns you to commit the `config.toml` change. If `local.toml` already has a different value, or the file layout is unusual, the skill stops and tells you what to move by hand.

**`executePrefs` and `automation`.** Both sections are in `.sdlc-v2/local.toml`, and they do different jobs. `automation.mode` gates whether `/sdlc:ship` hands control to `/sdlc:execute`. `executePrefs` governs the prompts inside `/sdlc:execute` once it has control.

## Follow-up instructions

When all tasks completed, a standalone run does the execute list of the
[follow-up instructions](../../instructions.md) before it marks the run
completed. The run mode of each instruction is the auto mode that the skill
resolves at its start.

| Part of the run | Before | After |
|---|---|---|
| End of a run | Step 9 report, then cleanup | Step 9 report, then the Step 9a list, then cleanup, then the summary line |

```text
Instructions (execute): 2 total, 1 done, 1 failed, 0 not run.
- `execute #1 (team) failed: <cause>`
```

The run state keeps each result. A small plan that runs directly writes no
state: the skill runs the list in the session and keeps no record.

| Exit | The list runs | Next |
|---|---|---|
| a config error in `[instructions]` at Step 0 | no | the error and its fix. No wave runs and no state is written. Correct the file, then run the skill again. |
| a wave `cancel` | no | the state stays resumable |
| a failed or partial run | no | the state stays resumable, and the instructions stay pending |
| all tasks completed | yes, before cleanup | cleanup, then the summary line |
| the run stops in the middle of the list | partly | the state stays in flight. `--resume` marks the running instruction `failed` with `result not recorded, not run again`, runs the rest of the list, then marks the run completed. |

Back to the overview: [/sdlc:execute](../execute.md#more-examples).
