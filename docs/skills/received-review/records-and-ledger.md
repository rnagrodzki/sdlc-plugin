# Records and the ledger

Read this page when a run of `/sdlc:received-review` left a finding unfixed and
you want to know where the finding is, or when you read the ledger at the end
of a run.

The run ends with a ledger. It accounts for every finding of the run:

```
Review findings: 14 total = 9 fixed + 5 deferred
  needs-direction  2
  disagree         2
  wont-fix         1
Run /sdlc:deferred to act on the 5 deferred findings.
```

The [overview page](../received-review.md#example) shows the run that prints it.

## The ledger

The ledger counts each finding in one of these terms:

| Term | Meaning |
|---|---|
| `fixed` | The fix is made and passed its check. |
| `deferred` | The finding is unfixed and has a record in the deferred backlog. The ledger groups these findings by `reason`. |
| `skipped at Step 10` | Manual mode only. You answered `skip` at the Step 10 prompt. The finding gets no fix, no reply and no record. |
| `UNACCOUNTED` | The finding is unfixed and no record could be written. The ledger names each such finding. |

The `Run /sdlc:deferred` line shows only when the deferred count is not 0.

`skipped at Step 10` counts each finding that you skipped at the Step 10
prompt, whether or not pre-approved findings got their fix (see
[Consent and auto-apply severities](./consent-and-auto-mode.md#consent-and-auto-apply-severities)).
The term shows only when its count is not 0. The summary also gets one line
for these findings:

```
- 11 findings skipped at the Step 10 prompt (no fix, no reply, no record)
Review findings: 14 total = 3 fixed + 0 deferred + 11 skipped at Step 10
```

The skill never changes the total to make the line balance. When the terms do
not add up to the total, the same line states the gap:

```
Review findings: 14 total = 9 fixed + 3 deferred — 2 UNACCOUNTED. Names: <file:line>, <file:line>.
```

## Records of unfixed findings

Every finding that ends unfixed gets a record, in manual mode and under
`--auto`. A finding that you skipped at the Step 10 prompt is the one
exception. The records show up in the deferred backlog that
[/sdlc:deferred](../deferred.md) triages.

**The `reason` field keeps the judgment.** A record carries a `reason`:
`wont-fix`, `disagree` or `needs-direction`. Under `--auto` every unfixed
finding has the outcome `needs-direction`, and the `reason` still says which
judgment the skill reached. So the person who reads the backlog can tell a
finding that the skill thought was wrong from a real choice between
approaches. [Auto mode](./consent-and-auto-mode.md#auto-mode) describes the
outcomes.

**The skill writes the records when the verdicts are final.** It does not
write a record at the moment it first reaches a verdict. A verdict can still
change later in the run, and the deferred file is append-only, so an early
write would leave a stale entry. One exception: under `--auto`, an unclear
comment is recorded at Step 2, because it leaves the run there.

**Recording is best-effort, with a fallback.** The primary write attaches the
finding to the current `/sdlc:ship` run. When the branch has no ship state, for
example in a standalone run, or when the write cannot be persisted, the skill
writes straight to the deferred file. Only when both writes fail does the
ledger name the finding `UNACCOUNTED`. The skill never drops a finding
silently.

**A reply claims tracking only when it is true.** The reply to a recorded
finding says that the finding is tracked as a deferred follow-up. The skill
leaves that sentence out when the record failed.

## A finding that is already deferred

A finding can arrive with a `**Deferred-id:**` line. The line means the finding
is already a deferred item. `/sdlc:ship` sends such findings in the fix pass of
its [`harden` step](../ship/pipeline-steps.md#harden). This skill fixes the
finding or leaves it deferred under the same id. It never defers the finding a
second time, so the backlog gets no duplicate. On a live `/sdlc:ship` run, a
fix resolves the deferred item. The summary lists each finding that stays
deferred as `<file:line> stays deferred as <id>`. The ledger counts such a
finding as deferred, in the group `below-threshold`.

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
The [dashboard](../../dashboard.md) shows these rows in the review queue of
the `review` step. It shows them on the received-review step when the run
has no review queue. The rows are for display only: the review ledger counts
the separate `fixed` records.

The writes need a live `/sdlc:ship` run. In a standalone run, the first call
reports that no run is live, and the skill makes no more status writes.

One run stores at most 200 fix records. No setting changes this cap. At the
cap, the tool rejects a new finding with an error that says "Do not retry".
The skill then makes no more `fix-progress` calls in the run. The same stop
applies to a damaged ship state. The fixes, the records and the replies
continue. The `fixed` and `defer` calls still set the rows that are already
stored, so a stored row can still move to `fixed` or `deferred` after the
stop.
