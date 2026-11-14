# Pipeline steps

Read this page when a step of `/sdlc:ship` skips, fails or asks a question, and you want the rule behind it. The short table of all steps is on the [overview page](../ship.md).

## Example

```
/sdlc:ship --dry-run
```

Prints the pipeline table (steps, arguments, pauses) and stops. Nothing runs, so you see which steps your configuration turns on.

## Default pipeline steps

**execute → commit → review → archive-openspec → pr → learnings-commit**

The order is fixed. Change the set of steps with [/sdlc:setup](../setup.md) or with `--steps` (see [Flags](./flags.md)).

- [Opt-in steps](#opt-in-steps) run only when you turn them on.
- [Conditional steps](#conditional-steps) run on their own when a review has findings to fix.
- A rebase runs between review and the next step. See [rebase](#rebase).
- A final cleanup always runs after the last configured step.

### commit

The `commit` step first stages the changes (everything except the `.sdlc-v2/`
data directory) and checks the working tree. When changes are staged, ship
runs [/sdlc:commit](../commit.md). When the tree is clean, the step completes
at once and `/sdlc:commit` does not run. The step result is then one of two
lines:

- `nothing to commit: …` — HEAD has not moved since the first commit check of
  this run. The line counts the waves that `/sdlc:execute` committed itself, or
  says the working tree is clean when `/sdlc:execute` committed no waves. Ship
  also adds it to the decisions log.
- `committed <sha>` — a commit landed since the first commit check of this run,
  for example when an interrupted run resumes after `/sdlc:commit` finished.
  Ship records that commit in the side-effect journal of the saved state.

A run with `--steps` that leaves out `execute` follows the same rules.

### rebase

A rebase onto the base branch (`[git] baseBranch`, else the repository default
branch) happens automatically between review and the next step. Skip it with
the `rebase` setting in `/sdlc:setup`. The rebase runs after any review-fix
commits and before the next configured step, so every later commit lands on
the rebased branch.

### archive-openspec

`archive-openspec` archives the OpenSpec change named by `--openspec-change`
or the plan's `**Source:**` header (or an auto-detected active change). It
skips when no change is named or detected. It calls the `ship_state` actions
`openspec-check` and `openspec-tasks`. The tools do the file checks, the
validation and the `tasks.md` box count. The skill does not run them itself.

- The change is already archived: the step completes with the result
  `already archived`.
- The named change is missing on disk, lacks `proposal.md` or a
  `specs/*/spec.md` file (missing artifacts), or fails
  `openspec validate --strict`: the action writes the fail row and the
  pipeline stops. The step prints the next action, the validate output, and
  the specs that a newer archived change edited (when there are any). The
  missing-on-disk message is the same as in `verify-openspec`.
- Outside `--auto`, the step asks for consent before every archive, also when
  all boxes of `tasks.md` are checked. With consent, it marks the open boxes
  done with `openspec-tasks` (when there are any), then archives. Under
  `--auto`, the step skips the question and does the same.
- A declined question records the decision `user declined archive` and skips
  the step.
- `openspec-check` or `openspec-tasks` returns an error: the step fails with
  the error text and prints the Suggestion. Fix the cause it names, then run
  `/sdlc:ship --resume`.
- `openspec archive` exits non-zero: the step fails with the CLI output. Fix
  the cause, then run `/sdlc:ship --resume`. The resumed step starts again,
  and the box sync finds no open box.

## OpenSpec checks at the start of a run

`ship_prepare` is the tool that starts a run. For a run with an OpenSpec
change, it does the checks below before any step runs.

### A plan with OpenSpec artifacts that are not authored

`ship_prepare` refuses a plan that names an OpenSpec change but whose
artifacts are not authored. This applies to a plan with an
`**OpenSpec-Create:**` line and no `**OpenSpec-Staging:**` or
`**OpenSpec-Saved:**` line. It also applies to a plan with an
`**OpenSpec-Staging:**` line whose OpenSpec Appendix starts with `[TBD`. A plan
with an `**OpenSpec-Saved:**` line is never refused by this check. The refusal
is two entries in `errors`: the message, then the fix. No state file is
written. Run `/sdlc:plan` on the plan again to finish the authoring, or remove
the `**OpenSpec-Create:**` and `**Source:**` lines to ship without OpenSpec.

### A staging folder that belongs to another plan

`ship_prepare` also refuses a plan whose staging folder belongs to another
plan. The `stage.json` of that folder names another plan file. The refusal is
two entries in `errors`: the message that names the owner plan, then the fix.
No file changes. Run ship with the owner plan, or pick another change name in
the plan header and run `/sdlc:plan` again. Only a run of the owner plan
deletes the staging folder.

### A spec shared with another change

When a run names an OpenSpec change, `ship_prepare` also checks for a shared
spec. The name comes from the staging header, the `**Source:**` header or
`--openspec-change`. `ship_prepare` compares the `specs/<capability>/` folders
of the change with those of every other active change and every other staged
change. Each shared capability gives one entry in `warnings`. The entry names
both changes and the capability. The warning gives you time to agree a merge
order with the other plan. A change that `ship_prepare` cannot read adds one
warning, with the read error, and does not stop the call. No action is needed
to ship. A dry run (`--dry-run`) runs no shared-spec check, so it shows none of
these warnings.

### The base commit of the change

For the same named change, `ship_prepare` stores the HEAD sha as
`openspecBaseHead` in the ship state file. A dry run stores nothing. When
`git rev-parse HEAD` fails, `ship_prepare` adds a warning and stores no key.

The only consumer is `openspec-check` (in `verify-openspec` and
`archive-openspec`), and only for the outcome `invalid`. It then returns
`overlaps`: each spec of the change that an archived change edited since
`openspecBaseHead`, for example after a rebase brought that archived change
in. With overlaps, the next action is to rebase the deltas of the change on
the new text of those specs. With no `openspecBaseHead` key, or a value that is
not a full commit sha, `openspec-check` skips this check and adds a warning.

## Opt-in steps

Set these steps to true in the step table of your project with `/sdlc:setup`,
or pass them with `--steps`.

### verify-openspec

Validates the current implementation against an active OpenSpec change before
archiving. It calls the `ship_state` action `openspec-check`, which checks the
files and runs the strict validation.

- Skipped automatically if there is no active change, even when configured.
- Completes when the change is already archived.
- Fails and stops the pipeline for three outcomes: the change is missing on
  disk, the change lacks `proposal.md` or a `specs/*/spec.md` file (missing
  artifacts), or `openspec validate --strict` fails (invalid).
- `openspec-check` returns an error (a domain, data or infra error): the step
  fails with the error text, prints the Suggestion and stops the pipeline. A
  person must look at the cause before `/sdlc:ship --resume`, also when the
  Suggestion asks for another call.
- For a change missing on disk with an `**OpenSpec-Saved:**` line in the plan,
  the failure message tells you to merge the OpenSpec PR (branch
  `openspec/<change>`) first, update the branch from the default branch, and
  run `/sdlc:ship --plan <plan-file>` again.

### harden

Groups the review findings into clusters and runs
[/sdlc:harden](../harden.md) on each one. It commits the guardrail,
review-dimension, and Copilot-instruction edits as a separate commit before
the PR. Then it runs one fix pass for the deferred findings that are below the
review threshold, with its own commit.

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

When the step runs:

- The step runs after `archive-openspec`. Outside `--auto`, it asks before
  each cluster.
- It skips when there are no review findings, when no cluster qualifies and no
  deferred finding is left to fix, or when the harden files already have
  uncommitted edits before the step starts.
- When `harden` is configured, `received-review` gets `--no-harden`, so
  hardening runs only once.

The fix pass:

- The fix pass sends the deferred findings to
  [/sdlc:received-review](../received-review.md), which fixes each one or
  leaves it deferred.
- Outside `--auto`, the fix pass pauses for you, as the received-review step
  does, unless `receivedReview.alwaysFixSeverities` pre-approves every finding.
- A fix pass that changes nothing ends as `Fix pass: 0 changes`, with no
  commit.
- The fix pass does not run when the working tree has uncommitted changes
  outside `.sdlc-v2/` and `openspec/`, so its commit holds only its own edits.

A failed fix pass ends in one of four ways:

- The pass fails, you stop it, or it returns no result, and it changed no
  file. The findings stay deferred, and the step completes.
- The pass fails after it changed files. Ship commits those files as the
  fix-pass commit. The findings that the pass did not fix stay deferred.
- The `git status` check of the working tree fails. The step fails, and ship
  dispatches nothing more.
- The staging or the commit of the fix-pass edits fails. The step fails. The
  findings that the pass already fixed stay fixed, and the edits stay in your
  working tree.

On `--resume`, a cluster already hardened is not run again, and a fix pass
that has its commit is not run again. If a resume finds uncommitted fix-pass
edits, ship asks whether to commit them. With `--auto` it fails the step
instead.

### verify-pipeline

Polls CI after the PR is created and analyzes or fixes failures. See
[/sdlc:verify-pipeline](../verify-pipeline.md).

### await-remote-review

Waits for an automated reviewer (for example Copilot) to respond.

## Conditional steps

These steps start automatically. They are not part of the step list that you
configure.

- `received-review` — Runs when review findings meet or exceed the configured
  severity threshold. Ship passes it every such finding verbatim. The run
  records it as a step. With no finding at or above the threshold, the step
  completes with 0 fixes.
- `commit-fixes` — Commits the fixes that `received-review` made, as a
  separate commit from your feature commit.

## The received-review step

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
| 1 | `M` is known and `P` is less than `M` | Asks whether to run `/sdlc:review` again. It calls no step action before you answer. Run again: review starts over. Continue: routes 2 to 6 with the parsed findings. With `--auto`, ship does not ask: it continues. | none yet |
| 2 | `K` is 1 or more | Starts the step, dispatches `/sdlc:received-review`, runs `commit-fixes` when `/sdlc:received-review` changed files, then ends the step. | `K finding(s) sent to received-review` |
| 3 | `K` is 0 and `M` is 0 | Starts the step and ends it. It dispatches nothing. | `0 fixes — no findings` |
| 4 | `K` is 0 and `M` is unknown | Starts the step and ends it. It dispatches nothing. | `0 fixes — no finding at or above the threshold` |
| 5 | `K` is 0, `M` is 1 or more, and `P` is `M` or more | Starts the step and ends it. It dispatches nothing. | `0 fixes — all M findings below threshold` |
| 6 | `K` is 0 and `P` is less than `M` (you chose Continue) | Starts the step and ends it. It dispatches nothing. | `0 fixes — parsed P of M findings, none at or above the threshold` |
| — | The dispatch fails, you stop it, it returns no result, or `commit-fixes` fails | Stops a worker that can still run with `TaskStop`. When that fails (ship runs nested under another dispatch), records the worker as possibly still running. Then calls `fail` with the reason. The step is `failed`, and the issue list of the run shows it. | none |

In the result text, `K`, `M` and `P` stand for the numbers of the run.

After the routes:

- The `await-remote-review` step can dispatch `/sdlc:received-review` again.
  That dispatch records a decision only, with `decide` on `received-review`.
  It makes no second `begin-step`, because that would set the completed step
  back to `in_progress`.
- On routes 3 to 6, ship dispatches nothing. A finding below the threshold is
  not lost: ship saves it for [/sdlc:deferred](../deferred.md). When the
  `harden` step is configured, it tries to fix these findings first.
- After the step, ship goes on with the rebase.

On `--resume`, the saved status of the step decides what ship does:

- `completed` — ship does not call `/sdlc:received-review` again. It goes on
  with the rebase.
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
