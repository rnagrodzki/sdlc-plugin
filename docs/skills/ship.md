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
  edits as a separate commit before the PR. It runs after `archive-openspec`.
  Outside `--auto`, it asks before
  each cluster. It skips when there are no review findings, when no cluster
  qualifies, or when the harden files already have uncommitted edits before
  the step starts. On `--resume`, a cluster already hardened is not run
  again. When `harden` is configured,
  `received-review` gets `--no-harden`, so hardening runs only once.
- `verify-pipeline` — Polls CI after the PR is created and analyzes or fixes
  failures.
- `await-remote-review` — Waits for an automated reviewer (e.g. Copilot) to
  respond.

Conditional steps — triggered automatically, not part of the step list you
configure:

- `received-review` — Runs when review findings meet or exceed the configured
  severity threshold. Ship passes it every such finding verbatim.
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

  An unbalanced ledger is a reporting bug worth seeing.

- **Review gaps.** When the review skips a dimension (the worker stalled twice
  or never checked in) or names a worker that may still be running, `/ship`
  records each one as a decision before the review step completes. The decisions log in this
  summary lists them.

- **Deferred follow-ups (Step 10b).** After the summary, `/ship` checks for
  deferred items that are still open — review findings that were saved rather
  than fixed, plus any issue drafts the execute step raised. If there are
  none, nothing is printed. If there are, it offers the same triage flow
  [/deferred](deferred.md) runs: file GitHub issues for the ones worth
  filing, resolve the rest, or leave them for later. With `--auto`, the open
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
plus the harden step's own commit when it ran), and the linked plan's
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
