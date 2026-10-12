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
| `--auto` | Suppress all confirmation prompts for the whole run. Two exceptions still ask: whether to resume or replace a run that is still in flight on this branch — see [State and Resolution Trace](#state-and-resolution-trace) — and filing a GitHub issue for an execute-step draft — see [End-of-run summary](#end-of-run-summary). | off |
| `--steps <csv>` | Comma-separated set of steps to run, overriding the project's step table. The plugin sorts them into the fixed order. | from config |
| `--quick` | Run the steps set to true in the project's `[ship.quick]` table. Does nothing if no quick table is configured. | off |
| `--quality <level>` | Quality tier forwarded to the execute step: `full`, `balanced`, or `minimal`. | unset (execute decides) |
| `--bump <level>` | Release bump forwarded to the PR step: `patch`, `minor`, `major`, or a pre-release label. | `patch` |
| `--draft` | Create the PR as a draft. Has no effect when the branch already has an open PR: that PR is updated, not converted. | off (or from config) |
| `--dry-run` | Print the pipeline table (steps, arguments, pauses) and stop without running anything. | off |
| `--resume` | Resume from saved progress after an interruption. | off |
| `--gc` | Clean up old saved-progress files and stop. Does not run the pipeline. | off |
| `--ttl-days <N>` | Retention period, in days, used by `--gc`. | `14` |
| `--openspec-change <name>` | The OpenSpec change to validate and archive during the pipeline. | auto-detected |
| `--init-config` | Redirects you to `/setup`. Does not run the pipeline. | off |

### Default pipeline steps

**execute → commit → review → archive-openspec → pr → learnings-commit**

`archive-openspec` archives the OpenSpec change named by `--openspec-change`
or the plan's `**Source:**` header (or an auto-detected active change). It
skips when no change is named or detected, and when the change is already
archived. It fails and stops the pipeline when the named change is missing on
disk (in both `openspec/changes/<name>/` and the archive) — the same check as
`verify-openspec`, with the same message. Outside `--auto`, it asks before
archiving a change whose `tasks.md` still has unchecked boxes.

The `commit` step first stages the changes (everything except the `.sdlc-v2/`
data directory) and checks the working tree. When changes are staged, `/ship`
runs [/commit](commit.md). When the tree is clean, the step completes at once
and `/commit` does not run. When the tree is clean, the step result is one of
two lines:

- `nothing to commit: …` — HEAD has not moved since the first commit check of
  this run. The line counts the waves that `/execute` committed itself, or says
  the working tree is clean when `/execute` committed no waves. `/ship` also
  adds it to the decisions log.
- `committed <sha>` — a commit landed since the first commit check of this run,
  for example when an interrupted run resumes after `/commit` finished. `/ship`
  records that commit in the side-effect journal of the saved state.

A run with `--steps` that leaves out `execute` follows the same rules.

A rebase onto the base branch (`[git] baseBranch`, else the repository default
branch) happens automatically between review and the
next step (skip it with the `rebase` setting in `/setup`). The rebase runs
after any review-fix commits and before the next configured step, so every
later commit lands on the rebased branch. A final cleanup always runs after the last configured step.

Opt-in steps — set them to true in your project's step table with `/setup` (or pass
them via `--steps`):

- `verify-openspec` — Validates the current implementation against an active
  OpenSpec change before archiving. Skipped automatically if there is no
  active change, even when configured. Fails and stops the pipeline when the
  plan names a change that is missing on disk. When the plan has an
  `**OpenSpec-Saved:**` line, the failure message tells you to merge the
  OpenSpec PR (branch `openspec/<change>`) first, update the branch from the
  default branch, and run `/ship --plan <plan-file>` again.
- `harden` — Groups the review findings into clusters and runs `/harden` on
  each one. It commits the guardrail, review-dimension, and Copilot-instruction
  edits as a separate commit before the PR. Then it runs one fix pass for the
  deferred findings that are below the review threshold, with its own commit.
  The fix pass sends those findings to [/received-review](received-review.md),
  which fixes each one or leaves it deferred. Outside `--auto`, the fix pass
  pauses for you, as the received-review step does. A fix pass that changes nothing
  ends as `Fix pass: 0 changes`, with no commit. The fix pass does not run
  when the working tree has uncommitted changes outside `.sdlc-v2/` and
  `openspec/`, so its commit holds only its own edits. A failed fix pass ends
  in one of four ways:
  - The pass fails, you stop it, or it returns no result, and it changed no
    file. The findings stay deferred, and the step completes.
  - The pass fails after it changed files. `/ship` commits those files as the
    fix-pass commit. The findings that the pass did not fix stay deferred.
  - The `git status` check of the working tree fails. The step fails, and
    `/ship` dispatches nothing more.
  - The staging or the commit of the fix-pass edits fails. The step fails. The
    findings that the pass already fixed stay fixed, and the edits stay in your
    working tree.

  The step runs after `archive-openspec`. Outside `--auto`, it asks before
  each cluster. It skips when there are no review findings, when no cluster
  qualifies and no deferred finding is left to fix, or when the harden files
  already have uncommitted edits before the step starts. On `--resume`, a
  cluster already hardened is not run again, and a fix pass that has its commit
  is not run again. If a resume finds uncommitted fix-pass edits, `/ship` asks
  whether to commit them. With `--auto` it fails the step instead. When
  `harden` is configured,
  `received-review` gets `--no-harden`, so hardening runs only once.

  ```mermaid
  flowchart LR
    A1[Review and review fix step] --> A2[Below-threshold findings deferred]
    A2 --> A3[Clusters and fix-now list]
    A3 --> A4[Harden per cluster]
    A4 --> A5[Harden commit]
    A5 --> A6[One fix pass for fix-now]
    A6 --> A7[Fix-pass commit]
    A7 --> A8[PR]
  ```

- `verify-pipeline` — Polls CI after the PR is created and analyzes or fixes
  failures.
- `await-remote-review` — Waits for an automated reviewer (e.g. Copilot) to
  respond.

Conditional steps — triggered automatically, not part of the step list you
configure:

- `received-review` — Runs when review findings meet or exceed the configured
  severity threshold. Ship passes it every such finding verbatim. The run
  records it as a step. With no finding at or above the threshold, the step
  completes with 0 fixes.
- `commit-fixes` — Commits the fixes `received-review` made, as a separate
  commit from your feature commit.

### The received-review step

When `review` is a configured step, the saved state of a run has a
`received-review` step right after `review`. The step starts as `pending`.
It does not block a later step while it is `pending`.

Ship records the step with two calls. `begin-step` starts it, and
`complete-step` ends it. On routes 2 to 6 below, ship makes both calls, also
when nothing needs a fix. A run that fails in this step calls `fail` in
place of `complete-step`.

Ship counts three numbers from the review output:

- `K` — the findings at or above `flags.reviewThreshold`.
- `M` — the total of findings in the header of the review output.
- `P` — the finding headings that ship parsed.

Ship checks the routes in this order. The first route that matches wins.

| Route | When | What ship does | Result of `complete-step` |
|---|---|---|---|
| 1 | `M` is known and `P` is less than `M` | Asks whether to run `/review` again. It calls no step action before you answer. Run again: review starts over. Continue: routes 2 to 6 with the parsed findings. With `--auto`, ship does not ask: it continues. | none yet |
| 2 | `K` is 1 or more | Starts the step, dispatches `/received-review`, runs `commit-fixes` when `/received-review` changed files, then ends the step. | `K finding(s) sent to received-review` |
| 3 | `K` is 0 and `M` is 0 | Starts the step and ends it. It dispatches nothing. | `0 fixes — no findings` |
| 4 | `K` is 0 and `M` is unknown | Starts the step and ends it. It dispatches nothing. | `0 fixes — no finding at or above the threshold` |
| 5 | `K` is 0, `M` is 1 or more, and `P` is `M` or more | Starts the step and ends it. It dispatches nothing. | `0 fixes — all M findings below threshold` |
| 6 | `K` is 0 and `P` is less than `M` (you chose Continue) | Starts the step and ends it. It dispatches nothing. | `0 fixes — parsed P of M findings, none at or above the threshold` |
| — | The dispatch fails, you stop it, it returns no result, or `commit-fixes` fails | Stops a worker that can still run with `TaskStop`. When that fails (ship runs nested under another dispatch), records the worker as possibly still running. Then calls `fail` with the reason. The step is `failed`, and the issue list of the run shows it. | none |

In the result text, `K`, `M` and `P` stand for the numbers of the run. The
`await-remote-review` step can dispatch `/received-review` again. That
dispatch records a decision only, with `decide` on `received-review`. It
makes no second `begin-step`, because that would set the completed step back
to `in_progress`. On
routes 3 to 6, ship dispatches nothing. A finding below the threshold is not
lost: ship saves it for [/deferred](deferred.md). When the `harden` step is
configured, it tries to fix these findings first. After the step, ship goes on
with the rebase.

On `--resume`, the saved status of the step decides what ship does:

- `completed` — ship does not call `/received-review` again. It goes on with
  the rebase.
- `pending`, `in_progress` or `failed`, after a completed `review` — ship
  does not run the routes again. The resumed session no longer has the
  review output, and a second deferral of a finding makes a duplicate entry.
  When a fix is still in progress and the tree has uncommitted changes, ship
  fails the step and stops, so you can check the changes. Otherwise it
  commits finished fixes, records a decision, and ends the step with
  `resumed — review output lost`. The findings that have no fix or deferral
  record show as `UNACCOUNTED` in the summary. Run `/sdlc:review` and then
  `/sdlc:received-review` to get them back. With `--auto`, ship does the
  same and asks nothing.

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

## State and Resolution Trace

Every `/ship` run reads its saved state before doing anything else — even
when `--resume` isn't passed — because a stale in-flight run can exist from
a prior session. What happens next depends on what it finds:

- **No saved state for this branch** (the read reports that no state file
  exists — this is not an error), or a finished run, or a run where no step
  ever started: a fresh run starts.
- **A run still in flight, with `--resume` or without `--auto`:** `/ship`
  shows where that run stopped and continues it. A plain `/ship` resumes too.
- **A run still in flight, with `--auto` but no `--resume`:** `/ship` shows
  where that run stopped and asks whether to resume it or start fresh. It
  asks even under `--auto`, because starting fresh deletes the old run's
  state file and it cannot be recovered.

The saved state file (one per branch, under `.sdlc-v2/`)
records, alongside the resolved pipeline flags:

- **`sources`** — a per-flag resolution trace: which precedence tier won for
  each resolved flag. Values include `"cli"`, `"config"`, `"quick"`,
  `"default"`, and, for the bump flag specifically,
  `"config (version.preReleasePolicy enforced over cli)"` when
  `preReleasePolicy: "always-rc"` overrides an explicit `--bump`. Useful for
  answering "why did ship pick this bump/quality level?" after the fact.
- **`versionCfg`** — a snapshot of the `[version]` config section as read
  at flag-resolution time, for diagnosing version/bump issues without
  needing to reconstruct what the config looked like when the run started.
- **`binaryVersion`** — the sdlc binary's build metadata (plugin version,
  commit, build time), so a state file can be traced back to the binary
  that produced it.
- **`reportData`** — step counts (total/completed/pending/skipped/failed),
  duration, decisions, deferred-finding count, bump provenance, the
  self-healing record (`healing`: fixed findings, fix progress of each finding, and harden runs, `{}` when
  none were recorded), and the review ledger (below), computed on the fly each time state is read (not
  persisted to disk). This lets the final pipeline report be rendered from
  already-computed values instead of re-deriving them from raw step/decision
  arrays.

None of this requires extra flags or setup — it's recorded (or derived)
automatically by every run and read back automatically on resume.

### End-of-run summary

After the last step, `/ship` prints the step table, the decisions log and the
deferred findings. Three parts of that summary are worth knowing about:

- **Review ledger.** When the review step ran, the summary closes with one
  ledger that accounts for *every* finding the review produced — how many
  were fixed by `received-review`, and how many were deferred, grouped by the
  reason each was deferred with:

      Review ledger: 14 = 9 fixed + 5 deferred (below-threshold: 3, needs-direction: 2)
      Run /sdlc:deferred to act on the 5 deferred finding(s).

  Every number here is read from the saved state's review ledger, never
  recomputed by `/ship` itself. The numbers are meant to add up. When they
  don't, `/ship` prints a second line instead of quietly adjusting the total:

      UNACCOUNTED: 2 finding(s) have no fix or deferral record

  An unbalanced ledger is a reporting bug worth seeing. A deferred finding that
  the `harden` fix pass fixes counts as fixed, not as deferred.

- **Review gaps.** When the review skips a dimension (the worker stalled twice
  or never checked in) or names a worker that may still be running, `/ship`
  records each one as a decision before the review step completes. The decisions log in this
  summary lists them.

- **Deferred follow-ups (Step 10b).** After the summary, `/ship` checks for
  deferred items that are still open — review findings that were saved rather
  than fixed, plus any issue drafts the execute step raised. If there are
  none, nothing is printed. If there are, it offers the same triage flow
  [/deferred](deferred.md) runs: file GitHub issues for the ones worth
  filing, resolve the rest, or leave them for later. A `harden-pending` item
  (rule edits that `/harden --auto` held back) is not filed and not resolved.
  For it, `/ship` prints the `/sdlc:harden` command that runs the rule edits
  and the `/sdlc:deferred --resolve <id>` command to run after it. The item
  stays open. With `--auto`, the open
  items are still listed but the triage question is not asked — they stay
  open for a later run. The execute-step issue drafts are the exception: they
  are offered even under `--auto`, because creating a GitHub issue always
  needs your approval. Nothing is lost either way — every deferred item is
  already written to disk when it is created.

### Run report

Near the end of a run, `/ship` calls the tool for a run report — one call
composes it from ship state, self-healing records, this run's execute
state, CLI evidence, user input, and learnings, then renders and writes it. This write
happens before the terminal cleanup step runs, because cleanup first copies the plan explorer
summary into ship state, then deletes the linked plan run that the report still needs to read. It covers
per-wave task outcomes, step timings, CLI evidence, drift/error/warning
counts, guardrail hits, any deferred findings or pending issue drafts, the
review ledger, a Review waves table, self-healing changes (fixes and hardening this run recorded,
plus the result of the harden step, with its own commit and the fix-pass
commit when they ran), and the linked plan's
planning time (start to its last edit — this ends at the last time the plan
file was modified, not at the moment the plan was accepted, so it reflects
time actually spent on the plan). The Review waves table has one row for each
dimension the review planned, with its wave, status, findings, and duration. It
is read from the review run that carries this ship run's ID. When there is no
such review run, the run plans no dimensions, or the review run cannot be read,
the section is one line that gives the reason. The Markdown report opens with a Summary
table that holds every run metric in one place; the sections below it show
counts instead of one line per command, and list only the findings and
failures a person must act on. It also lists every prompt you typed and every question you answered while
the run was active, so a correction or a choice made mid-run is visible
after the run ends. An old or injected turn (for example an editor
notification) is filtered out of that list and never counted. It is gated by
the project's
`automation.report` config (`enabled`, and `format`: `"json"` or `"md"`,
default `"md"`). When enabled, the tool
persists it under `.sdlc-v2/reports/ship-<runId>-report.{json,md}` in the
main worktree (not wherever the session happens to be running from, e.g. a
linked worktree) and `/ship` prints that path as the last line of that
step's output.
`/ship` never renders or writes the report itself — the tool always does
both. Disabled reporting (`automation.report.enabled = false`) skips this
step silently — nothing is written.

## Related skills

- [/plan](plan.md) — Creates the plan file for the execute step.
- [/execute](execute.md) — First step of the pipeline (when a plan is given).
- [/commit](commit.md) — Commit step. It runs only when the working tree has
  changes to commit.
- [/review](review.md) — Review step.
- [/harden](harden.md) — Opt-in `harden` step: turns clustered review
  findings into guardrail, review-dimension and Copilot-instruction edits.
  It then runs one fix pass for the deferred findings below the review
  threshold.
- [/pr](pr.md) — PR creation step.
- [/verify-pipeline](verify-pipeline.md) — Optional post-PR CI verification.
- [/deferred](deferred.md) — Triage the findings and issue drafts a run left
  open.
- [/setup](setup.md) — Configure pipeline steps and settings.

## Tips and gotchas

- **Configure before first use.** Run `/setup` to set the step table, review
  threshold, release bump, and more.
- **`--quick` needs configuration.** It uses the `[ship.quick]` table from your
  project config. Nothing happens if no quick table is set up.
- **Review threshold.** Default: `info` — every finding, including Info,
  triggers the fix loop. Change it via `/setup`. Findings below a higher
  threshold are saved for [/deferred](deferred.md), not dropped.
- **Saved progress survives sessions.** Run `/ship --resume` (or a plain
  `/ship`) in a new session to continue an interrupted pipeline.
- **Two independent automation controls.** `--auto` (and its config
  equivalent) suppresses confirmation prompts for the whole run. A separate
  per-step automation setting in your project config controls whether
  individual steps pause even when `--auto` is off — the two do not imply
  each other, so check both if a step pauses (or doesn't) unexpectedly.
- **Promoting an RC to final doesn't need a new PR.** Use the "Promote Release" GitHub
  Actions workflow (Actions → Promote Release → Run workflow) to turn the latest RC tag
  into a final release at the same tested commit. See [versioning docs](../versioning.md#promoting-rc-to-final-release).
