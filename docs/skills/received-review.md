# /received-review

Respond to code review feedback on a pull request. Reads reviewer comments,
verifies each one against the actual code, and applies fixes or replies with
technical justification.

## When to use

- A reviewer (human or automated) left comments on your PR and you want to
  work through them systematically.
- `/ship` triggered an automatic fix loop because the review found issues at
  or above the project's configured severity threshold.
- You want review feedback addressed with technical rigor, not surface-level
  agreement.

## Syntax

    /received-review [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--pr <number>` | PR number to process feedback for. | auto-detected from branch |
| `--auto` | Run without prompts. Only "agree — will fix" ends a finding; every other outcome is recorded as a `needs-direction` follow-up instead of being closed. See [Auto mode](#auto-mode). | off |
| `--no-harden` | Skip the harden dispatch in Step 11.6. Use when a caller (e.g. `/ship`'s own `harden` step) already owns hardening for this run. | off |

## Examples

**Process feedback on the current branch's PR:**

    /received-review

Detects the PR, reads all comments, verifies each against the code, and works
through them one by one.

**Process feedback for a specific PR:**

    /received-review --pr 42

**Process feedback without prompts (used by pipelines):**

    /received-review --auto

## Auto mode

`--auto` is for pipeline and subagent dispatch, where there is no human to
ask. It does not mean "agree with everything and move on". It narrows what is
allowed to close a finding down to one verdict:

- **`agree — will fix`** — the only outcome that ends a finding. The fix is
  made and the review thread gets a reply saying so.
- **Everything else becomes `needs-direction`** — recorded, not closed.
  "Agree, won't fix", "disagree", "cannot verify", and comments too unclear to
  evaluate all land here. Without `--auto`, an unclear comment stops the whole
  run to ask you; under `--auto` there is no one to ask, so it is recorded
  instead — with a note on what's unclear — and the run keeps going with the
  rest of the comments. The thread gets a reply naming the open question, and
  the finding goes to the backlog.

**A fix that fails its own check is reverted, not retried blindly.** If a
change doesn't compile or doesn't pass its tests, `--auto` undoes just that
change and records it as `needs-direction` with the failure noted, instead of
guessing at a second approach with no one watching.

Either way the thread itself is only replied to, never resolved: this port
does not resolve review threads programmatically, so close them yourself in
the GitHub UI once you are satisfied.

A recorded finding keeps the judgment that was actually reached, in a separate
`reason` field (`wont-fix`, `disagree` or `needs-direction`), so whoever reads
the backlog later can tell a finding the skill thought was wrong from one that
is a real fork in the road.

`needs-direction` is only valid when the record names **two or more** candidate
approaches plus a one-line trade-off between them. One obvious approach is not
a question — it is a fix.

**Where the records go.** They are written once verdicts are final, not at the
moment a verdict is first reached — a verdict can still change later in the
run, and the deferred file is append-only, so an early write would leave a
stale entry behind. They show up in the deferred backlog that
[/deferred](deferred.md) triages, and in the end-of-run ledger that accounts
for every finding as `fixed`, `deferred` (grouped by `reason`),
`skipped at Step 10`, or `UNACCOUNTED`.

**Recording is best-effort, with a fallback.** The primary write attaches the
finding to the current `/ship` run. When there is no ship state for the branch
— a standalone run, for instance — or the write cannot be persisted, the skill
falls back to writing straight to the deferred file instead. Only if both fail
is the finding named `UNACCOUNTED` in the ledger. It is never silently dropped.

**No PR yet? No reply step.** When there's no PR number — for example when
`/ship` dispatches this skill on its fix loop before ever opening one — there
are no review threads to reply to. The reply-posting and reply-verification
steps are skipped outright; only the run's summary and ledger are shown.

**A finding that is already deferred.** A finding can arrive with a
`**Deferred-id:**` line. The line means the finding is already a deferred item.
`/ship` sends such findings in the fix pass of its [`harden` step](ship.md).
This skill fixes the finding or leaves it deferred under the same id. It never
defers the finding a second time, so the backlog gets no duplicate. On a live
`/ship` run, a fix resolves the deferred item. The summary lists each finding
that stays deferred as `<file:line> stays deferred as <id>`.

**It also reaches `/harden`.** Step 11.6 clusters the findings by file and
dispatches [`/harden --auto`](harden.md) for each cluster (capped at 5), which
applies guardrail and review-dimension edits (add, tighten, repair, and removal
of a rule with a dead reference) to your project without a confirmation prompt.
Merges and other removals wait as pending deferred items, as
[`/harden`](harden.md#auto-mode-and-pending-edits) describes. Each applied edit
is listed under "Auto-accepted", and each pending edit under "Pending", in the
summary this skill relays back. Pass
`--no-harden` to skip this dispatch — for example when `/ship`'s own `harden`
step already owns hardening for the run.

## Consent and auto-apply severities

Set `receivedReview.alwaysFixSeverities` in `.sdlc-v2/local.toml` (or `~/.sdlc/local.toml`).
Allowed values: `critical`, `high`, `medium`, `low`, `info`. Default: `[]` (every finding asks).

| Situation | What happens |
|---|---|
| `--auto` | Every "will fix" finding gets its fix. The setting adds nothing. |
| A "will fix" finding with a listed severity | It gets its fix with no prompt. One `pre-approved` line shows it. |
| A comment with no severity | It counts as `medium`. |
| Other findings exist | One prompt asks about them only. |
| You answer skip | Only the pre-approved findings get their fix. Skipped findings get no fix, no reply and no record. |
| You answer edit | The skill shows the plan again and computes the pre-approved findings again. |
| Every finding is pre-approved | No prompt. |
| No finding needs an action | No prompt. Only the summary shows. |
| The local file cannot be read | The error shows, and every finding asks. |

The end-of-run ledger gets one more term: `skipped at Step 10` counts each finding that you skipped while pre-approved findings existed. The term shows only when its count is not 0.

## Fix progress on the dashboard

In every mode, Step 11 writes the status of each accepted finding (`queued`,
`fixing`, `fixed`, `failed`, `deferred`) to `healing.fixProgress` of the ship
state. Two other calls also set the matching row:

- `ship_state` `healing_record` with `kind:"fixed"` sets the row to `fixed`.
- `ship_state` action `defer` sets the row to `deferred` and copies the
  deferred id onto it. It does this only when its `deferred.json` write
  succeeded. A `failed` row stays `failed`.

Each of these calls changes a row only when exactly one row matches the
finding. It never creates a row. So when the skill skips a `fix-progress`
call, the row still moves on at the `fixed` or `defer` call.
The [dashboard](../dashboard.md) shows these rows in the review queue of
the `review` step. It shows them on the received-review step when the run
has no review queue. The rows are for display only: the review ledger counts
the separate `fixed` records.

The writes need a live `/ship` run. In a standalone run, the first call
reports that no run is live, and the skill makes no more status writes.

One run stores at most 200 fix records. No setting changes this cap. At the
cap, the tool rejects a new finding with an error that says "Do not retry".
The skill then makes no more `fix-progress` calls in the run. The same stop
applies to a damaged ship state. The fixes, the records and the replies
continue. The `fixed` and `defer` calls still set the rows that are already
stored, so a stored row can still move to `fixed` or `deferred` after the
stop.

## Related skills

- [/review](review.md) — Produces the findings that this skill responds to.
- [/commit](commit.md) — Commit the fixes after addressing feedback.
- [/ship](ship.md) — Runs this skill when review findings meet or exceed the
  severity threshold.
- [/deferred](deferred.md) — Triages the findings this skill records instead
  of fixing.
- [/harden](harden.md) — Dispatched per finding cluster in Step 11.6 to turn
  recurring findings into project guardrails.

## Tips and gotchas

- **Dual self-critique.** The skill verifies each comment against the actual
  code before accepting it. If a comment is incorrect, it explains why instead
  of blindly agreeing.
- **Not just for human reviewers.** This skill processes feedback from any
  source: humans, Copilot, or `/review`'s own findings.
- **Fix loop in /ship.** When running inside `/ship`, this skill triggers
  conditionally — only if `/review` produced at least one finding that meets
  or exceeds the configured severity threshold. Findings below it are deferred
  instead, not dropped.
