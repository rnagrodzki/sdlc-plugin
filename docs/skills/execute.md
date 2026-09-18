# /execute

Implement a plan file by running tasks in optimized parallel waves, with
verification after each wave and automatic recovery from failures.

## When to use

- You have a plan file (from `/plan` or written manually) and want to
  implement it.
- You want tasks grouped into dependency-aware waves that run in parallel
  where possible.
- You want automatic error recovery — if a task fails, the skill retries or
  adjusts before stopping.

## Syntax

    /execute <plan-file-path> [options]
    /execute --plan <path> [options]
    /execute --resume [--plan <path>]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `<plan-file-path>` | Path to the plan file. Required unless resuming. | none |
| `--plan <path>` | Alternative way to specify the plan file path. | none |
| `--quality <level>` | Quality tier: `full`, `balanced`, or `minimal` (see table below). | interactive prompt |
| `--resume` | Resume from saved progress after an interruption. | off |
| `--rebase <mode>` | Rebase strategy: `auto`, `skip`, or `prompt`. | `skip` |
| `--auto` | Skip all interactive prompts. | off |
| `--branch <name>` | Create and check out this branch before executing. | auto-derived |
| `--wave-timeout <s>` | Max seconds a single wave can run. Also used as each task's total-runtime ceiling when the server classifies still-open tasks. | `1800` |
| `--wave-interval <s>` | Seconds between wave-await liveness polls. Also sets a heartbeat-staleness threshold of 10x this value (default 600s) before a worker is considered stalled, and a reclaim grace of max(5x this value, 300s) (default 300s) before a worker that never answers the reclaim is failed. See [Execute wave supervision](../execute-wave-supervision.md). | `60` |

### Quality tiers

| Value | What it does |
|-------|-------------|
| `full` | Fastest and cheapest. Uses lighter models. Skips spec compliance review. |
| `balanced` | Default. Good balance of speed and correctness. |
| `minimal` | Slowest but highest correctness. Uses the strongest models throughout. |

## Examples

**Execute a plan with default settings:**

    /execute ~/.claude/plans/auth-redesign.md

Creates a feature branch (if on the default branch), classifies tasks, builds
waves, and starts executing. Asks you to pick a quality tier.

**Execute at full speed without prompts:**

    /execute --plan tasks/migrate-db.md --quality full --auto

Runs the fastest quality tier without stopping for confirmation.

**Resume after an interruption:**

    /execute --resume

Picks up from saved progress. The plan path is read from the saved progress
file — you do not need to pass it again.

## Related skills

- [/plan](plan.md) — Creates the plan files this skill consumes.
- [/commit](commit.md) — After execution, commit the changes.
- [/ship](ship.md) — Runs `/execute` as its first step, then continues with
  commit, review, and PR.

## See also

- [Execute wave supervision](../execute-wave-supervision.md) — how heartbeats,
  the three ceilings, reclaim, and retries actually work.

## Tips and gotchas

- **The plan file path is required.** The skill never guesses which plan to
  execute. Always pass it explicitly (except when resuming).
- **Branch handling.** If you are on the default branch (e.g., `main`), the
  skill creates a feature branch automatically. If you are already on a feature
  branch, it runs in place.
- **Wave structure.** Tasks are grouped into waves based on their dependencies.
  Independent tasks run in parallel within a wave. The skill pauses between
  waves flagged as high-risk to let you inspect the results.
- **Resuming is safe.** If the session ends mid-execution, run
  `/execute --resume` in a new session. Progress is saved after each completed
  wave.
- **State is always loaded first.** Regardless of `--resume`, the skill's
  first action is an unconditional `execute_state({action: "read"})` call —
  not just when resuming — so a stale in-flight run from a prior session is
  always detected before anything else happens.
- **Waves are supervised by a server-driven poll, not the session itself.**
  After dispatching a wave's tasks, the skill repeatedly calls
  `execute_state({action: "wave-await"})` on the `--wave-interval` cadence.
  The server classifies each still-open task, and its response's `next`
  field tells the skill exactly what to do next: keep polling, reclaim a
  stalled worker, redispatch a task that timed out or never answered its
  reclaim (via `execute_state({action: "task-redispatch"})`, up to two
  retries), or move on once the wave is done. A worker that *does* answer a
  reclaim is kept: the reply proves it is alive, so the same attempt
  continues and no retry is spent. The skill always follows `next` as given
  rather than re-deriving its own liveness logic.
