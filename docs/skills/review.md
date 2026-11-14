# /review

Run a multi-dimension code review of the current diff. Each dimension
(security, performance, documentation, etc.) is reviewed independently, and
findings are reported with severity levels.

## When to use

- You have uncommitted or committed changes and want a thorough code review
  before opening a PR.
- You want to catch issues across multiple quality dimensions (security,
  performance, error handling, etc.).
- You want to preview which dimensions would run without actually running the
  review (`--dry-run`).

## Syntax

    /review [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--base <branch>` | Compare against this branch instead of the auto-detected default. | auto-detected |
| `--dry-run` | Show the review plan (dimensions, file counts) without running it. Passed to `review_prepare` as `dryRun: true`, so the run writes no ledger. | off |

**Note:** The review scope is controlled by the `scope` key of the `[review]`
section in `.sdlc-v2/local.toml`, not by command-line flags. Accepted values:
`all` (the default), `committed`, `staged`, `working`, and `worktree`. `working`
covers all uncommitted changes to tracked files, staged and unstaged
(`git diff HEAD`). `worktree` compares the base branch with the working tree
(`git diff <base>`), so it covers the branch's commits plus uncommitted edits.
For `staged`, `working`, and `worktree` the review is never offered for posting
to a PR. Change the scope with `/setup`.

The `maxParallelDimensions` key of the same `[review]` section sets how many
review agents run at the same time (default 8, minimum 1, whole number).
Every matching dimension runs, most severe first, in waves of this size.
To change it, run `/setup`, or edit the `[review]` section of
`.sdlc-v2/local.toml` directly. The old key `maxDimensions` is an error.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0 — Run `review_prepare` | tool | Calls `review_prepare`, reads the manifest and shows each warning. On a tool error it shows the error and stops. | The manifest with the dimensions and the waves | no | always |
| Step 1 — Dry Run Check | skill | Prints the review plan, cleans up the files of the run and stops. No agent starts. | The review plan | yes | `--dry-run` is passed |
| Step 2 — Wave Dispatch | agent | Starts one background agent for each dimension, wave by wave, with at most `maxParallelDimensions` agents in a wave. | Workers that write their findings to the ledger | no | always, unless `manifest.waves` is empty |
| Step 3 — Poll Ledger Status (per wave) | tool | Polls `ledger_status` for each wave. It stops and skips a worker that stalls or is missing on two polls in a row. | The findings of each worker | no | always, unless `manifest.waves` is empty |
| Step 4 — Consolidate Findings (relocated critique/dedupe pass) | skill | Removes duplicates, marks contradictions and corrects wrong severities. It ignores the findings of a stopped worker. | The consolidated findings | no | always |
| Step 5 — Build and Persist Consolidated Comment | skill | Builds the review comment with the verdict and saves it as `review-comment.md` in the diff directory. | The comment file and the verdict | no | always |
| Step 6 — Display Full Comment Body | skill | Shows the full text of the comment in the terminal, with no summary. | The visible review | no | always |
| Step 7 — Handle Posting | skill | Checks the links in the comment, then posts it to the PR, saves it under `.sdlc-v2/reviews/` or keeps it in the terminal. | A posted comment or a saved review file | no | always. The options depend on the PR and the scope |
| Step 8 — Offer Self-Fix | skill | Offers `fix` (runs `received-review`), `harden` (only when the verdict is `CHANGES REQUESTED`) or `no`. | A `received-review` run, a `harden` run or nothing | yes | the verdict is `CHANGES REQUESTED` or `APPROVED WITH NOTES` |
| Step 9 — Cleanup | tool | Stops the workers that did not finish. Deletes the manifest file. Deletes the diff directory only when no worker could be left running. | A clean working state | no | always, on every end of the run |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/review`. `--base <branch>` sets the base branch. `--dry-run` shows the plan only. |
| Ends | After Step 9. A dry run ends after the cleanup of Step 1. |
| Asks the user | At Step 7: post, save or keep in the terminal. At Step 8: fix, harden or no. |
| Resume | none |

## Examples

**Review all current changes:**

    /review

Detects changed files, determines which dimensions apply, and runs one
reviewer per dimension, in waves of up to `maxParallelDimensions` agents.

**Preview the review plan:**

    /review --dry-run

Shows which dimensions would run and how many files each covers — without
running any reviews.

**Review against a specific branch:**

    /review --base develop

Compares the current branch against `develop` instead of the default.

## Precomputed report data

`/review`'s preparation step (`review_prepare`) returns `manifestPath` and a
`summary` with already-computed counts: `total_dimensions`,
`active_dimensions`, `skipped_dimensions`, `wave_count`,
`total_changed_files`, `uncovered_file_count`, and `suggested_dimensions`.
The manifest file at `manifestPath` repeats `summary` and adds `scope`,
`git` (`commit_count`, `changed_files_count`), `pr` (`exists`, and the PR
number and URL when one exists), `plan_critique` (uncovered files,
over-broad dimensions, `max_parallel_dimensions`), `waves`, and `run_id`. The
`run_id` names the ledger folder of the run. It is empty for `--dry-run` and
when there are zero waves. Each entry of `dimensions` carries a `worker_id`,
which names the ledger entry of that dimension. The skill reads both values and
builds neither. These describe
the review's *input* (what's being reviewed and with which dimensions) —
finding counts and severity breakdowns are **not** precomputed, since
findings don't exist until each dimension's reviewer lane actually runs;
those are aggregated after the lanes complete.

## Example dimension: `skill-doc-drift`

This project dogfoods its own review dimensions.
`.sdlc-v2/review-dimensions/skill-doc-drift.md` checks that each
`docs/skills/*.md` file stays in sync
with its `SKILL.md` source of truth — steps, tool calls, flags, and mandatory
gates like the state-first Step 1 described in [execute](execute.md) and
[plan](plan.md). It's a useful pattern to copy if you want docs-vs-
implementation drift caught automatically in your own project's reviews.

## Related skills

- [/received-review](received-review.md) — Respond to the findings from this
  skill.
- [/commit](commit.md) — Commit changes before or after reviewing.
- [/ship](ship.md) — Runs `/review` as its third step.
- [/setup](setup.md) — Configure review dimensions and scope.

## Tips and gotchas

- **Dimensions are project-specific.** Review dimensions come from
  `.sdlc-v2/review-dimensions/`. Run `/setup --dimensions` to add or change
  them.
- **Scope is config-driven.** You cannot pass `--staged` or `--committed` on
  the command line. The scope is set in `.sdlc-v2/local.toml`. Use `/setup` to
  change it.
- **Dry run is useful for tuning.** If reviews miss files or take too long, use
  `--dry-run` to see the plan and adjust dimensions accordingly.
- **Severity levels drive pipelines.** In `/ship`, findings at or above the
  configured threshold trigger an automatic fix loop. Lower-severity findings do
  not block. They are saved for [/deferred](deferred.md), not dropped.
- **The run ledger stays after the review.** Each run records its dimension
  workers in a ledger under `.sdlc-v2/runs/ledger/`. When the review ends, it
  stops each dimension worker that did not finish (`TaskStop`), then removes
  its manifest and its temp diff directory, but it keeps the ledger. Under
  `/ship`, the stop can fail; the review then names those workers as possibly
  still running. A worker that stalls or never checks in twice in a row is
  stopped, and the review records the reason with `execute_state`
  `ledger_skip` (`stalled`, `missing`, or `unstopped` when the stop failed or the
  worker has no task ID). The dashboard shows that dimension as skipped with
  the reason, until the worker checks out. A review that is
  stopped and run again gets a new `run_id`. Its old ledger folder shows as
  `stalled` after 30 minutes and stays until the sweep removes it. The
  [dashboard](dashboard.md) reads the ledger to show the review dimensions of a
  finished run. The first `execute_state` `gc` sweep or `/ship`
  `cleanup-pipeline` sweep after 7 days (the default `state.gc.ttlDays`)
  removes the ledger.
