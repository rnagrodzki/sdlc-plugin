# /ship

Run the full ship pipeline: execute a plan, commit, review, and open a pull
request — all in one command, pausing for confirmation between steps by
default. Optionally verify CI and wait for automated reviewer feedback.

## When to use

- You have a plan file and want to go from code to PR in one step.
- You want branching, committing, reviewing, and PR creation handled
  automatically.
- You want to resume a pipeline that was interrupted mid-run.
- You already have changes (no plan needed) and want to commit, review, and
  open a PR.

## Syntax

    /ship [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--plan <path>` | Plan file to execute. Omit to skip the execute step and start from commit. | none |
| `--auto` | Suppress all confirmation prompts for the whole run. | off |
| `--steps <csv>` | Comma-separated list of steps to run, overriding the project's configured step list. | from config |
| `--quick` | Use the project's configured shortcut step list. Does nothing if no shortcut list is configured. | off |
| `--quality <level>` | Quality tier forwarded to the execute step: `full`, `balanced`, or `minimal`. | unset (execute decides) |
| `--bump <level>` | Release bump forwarded to the PR step: `patch`, `minor`, `major`, or a pre-release label. | `patch` |
| `--draft` | Create the PR as a draft. **Not yet functional.** | off |
| `--dry-run` | Print the pipeline table (steps, arguments, pauses) and stop without running anything. | off |
| `--resume` | Resume from saved progress after an interruption. | off |
| `--gc` | Clean up old saved-progress files and stop. Does not run the pipeline. | off |
| `--ttl-days <N>` | Retention period, in days, used by `--gc`. | `14` |
| `--openspec-change <name>` | The OpenSpec change to validate and archive during the pipeline. | auto-detected |
| `--init-config` | Redirects you to `/setup`. Does not run the pipeline. | off |

### Default pipeline steps

**execute → commit → review → archive-openspec → pr → learnings-commit**

A rebase onto the default branch happens automatically between review and the
next step (skip it with the `rebase` setting in `/setup`). A final cleanup
always runs after the last configured step.

Opt-in steps — add them to your project's step list with `/setup` (or pass
them via `--steps`):

- `verify-openspec` — Validates the current implementation against an active
  OpenSpec change before archiving. Skipped automatically if there is no
  active change, even when configured.
- `verify-pipeline` — Polls CI after the PR is created and analyzes or fixes
  failures.
- `await-remote-review` — Waits for an automated reviewer (e.g. Copilot) to
  respond.

Conditional steps — triggered automatically, not part of the step list you
configure:

- `received-review` — Runs when review findings meet or exceed the configured
  severity threshold.
- `commit-fixes` — Commits the fixes `received-review` made, as a separate
  commit from your feature commit.

## Examples

**Ship with a plan file:**

    /ship --plan ~/.claude/plans/auth-redesign.md

Executes the plan, commits, reviews, and opens a PR. Pauses between steps
by default.

**Ship without prompts:**

    /ship --plan tasks/my-plan.md --auto

Full pipeline without stopping.

**Ship already-made changes (no plan):**

    /ship

Skips execute and starts with commit.

**Preview the pipeline:**

    /ship --dry-run

Shows which steps would run and where the pipeline would pause.

**Resume after interruption:**

    /ship --resume

Picks up from saved progress in a new session — no need to pass `--plan`
again.

## Related skills

- [/plan](plan.md) — Creates the plan file for the execute step.
- [/execute](execute.md) — First step of the pipeline (when a plan is given).
- [/commit](commit.md) — Commit step.
- [/review](review.md) — Review step.
- [/pr](pr.md) — PR creation step.
- [/verify-pipeline](verify-pipeline.md) — Optional post-PR CI verification.
- [/setup](setup.md) — Configure pipeline steps and settings.

## Tips and gotchas

- **Configure before first use.** Run `/setup` to set the step list, review
  threshold, release bump, and more.
- **`--quick` needs configuration.** It uses a shortened step list from your
  project config. Nothing happens if no shortcut list is set up.
- **Review threshold.** Default: `high` — only critical and high findings
  trigger the fix loop. Change it via `/setup`.
- **Saved progress survives sessions.** Run `/ship --resume` in a new session
  to continue an interrupted pipeline.
- **Two independent automation controls.** `--auto` (and its config
  equivalent) suppresses confirmation prompts for the whole run. A separate
  per-step automation setting in your project config controls whether
  individual steps pause even when `--auto` is off — the two do not imply
  each other, so check both if a step pauses (or doesn't) unexpectedly.
