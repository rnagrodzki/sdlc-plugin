# Ship state record

Read this page when you read the harden record of a ship run in the dashboard
or in the ship report, or when a record stays `started`.

When [`/sdlc:ship`](../ship.md) runs `/sdlc:harden` (the `--step "ship harden"`
argument), `/sdlc:harden` stores one record of the run in the ship state, in
`data.healing.hardened`. The dashboard and the ship report read it. A
standalone `/sdlc:harden` run stores nothing.

When the proposal loop runs, the run makes two `healing_record` calls with the
same `trigger`. The first call (step 5-pre, before the first proposal) stores a
`started` record. The second call (step 5e, after the last proposal) stores a
`done` record. It replaces the `started` record. When the proposal loop does
not run (the orchestrator drafted no proposal, or a `plugin-defect` result with
no repair proposal), the run makes one call: a `done` record, with no `started`
record before it.

## Fields

| Field | Description |
|-------|-------------|
| `phase` | `started` or `done` |
| `trigger` | The first 200 characters of the failure text. It is the key of the record |
| `classification` | `user-code`, `plugin-defect` or `ambiguous` |
| `applied[]` | One entry for each proposal that was written: `surface`, `action`, `targetFile`, `rationale?`, `patch?`. At most 100 entries |
| `applied[].rationale?` | The rationale of the proposal, at most 600 characters. The key is absent when the proposal has no rationale |
| `applied[].patch?` | The text that the write added. For a guardrail edit that a repair round changed, and for every `consolidate`, it is the TOML entry that was written. Otherwise it is the patch of the proposal. The key is absent when there is no text. A patch over 1536 bytes is cut, and the entry gets `patchCut: true` |
| `skipped` | The number of proposals that were not applied |
| `held[]` | One entry for each proposal that the run left for a person: `surface`, `action`, `targetFile`, `reason`, `deferredId?`. At most 100 entries |
| `held[].targetFile` | The target file of the proposal, or `(path rejected)` when the path is not a file of its surface. A held proposal was never written, so `/sdlc:harden` checks its path only for this record |
| `held[].reason` | `pending-stored`, `pending-not-stored` or `not-processed` |
| `held[].deferredId?` | The id of the deferred item. Only `pending-stored` has it |
| `recordedAt` | The time of the call |

`/sdlc:harden` keeps the written TOML of a repaired or `consolidate` guardrail
in the conversation only, until the `done` call. When the conversation loses it
(for example, after a context compaction), `applied[].patch` holds the patch of
the proposal, which can differ from the text that was written.

**Limits of one call.** A call with more than 100 `applied[]` entries, more
than 100 `held[]` entries, or a `rationale` over 600 characters fails with an
error and writes nothing. The `started` record then stays (see the causes
below). `/sdlc:harden` cuts a longer `rationale` to 600 characters before the call.
A `patch` over 1536 bytes does not fail the call: the tool cuts it, sets
`patchCut: true` and adds a warning to the response.

## Held proposals

The three reasons of `held`:

- `pending-stored`: `--auto` stored the proposal as a deferred item
  (see [Auto mode and pending edits](./auto-mode-and-edits.md#auto-mode-and-pending-edits)).
- `pending-not-stored`: `--auto` could not store the proposal. Copy it by hand
  from the summary.
- `not-processed`: a halt stopped the loop before the proposal.

What `held` holds depends on how the run ends. Each run matches exactly one row
of this table. A halt is a stop in the middle of the proposal loop:
`/sdlc:harden` could not restore a file after a failed edit, or it could not
update the Copilot mirror of a review dimension.

| Mode | How the run ends | `held` | What `/sdlc:harden` does next |
|---|---|---|---|
| `--auto` | every proposal processed | `pending-stored`, with its `deferredId`, for each pending edit that was stored. `pending-not-stored` for each pending edit that was not stored | the `error-report` route when the result is `plugin-defect`, then the learnings entry |
| `--auto` | a halt | the entries of the row above, plus `not-processed` for each proposal after the halted one | stops. No learnings entry |
| interactive | you decided every proposal | `[]` | the `error-report` route when the result is `plugin-defect`, then the learnings entry |
| interactive | a halt | `not-processed` for each proposal after the halted one | stops. No learnings entry |
| interactive | you chose **cancel** at the `Apply this proposal?` prompt | `[]` | stops. No learnings entry |
| any | the orchestrator drafted no proposal, or a `plugin-defect` result with no repair proposal | `[]`. The proposal loop did not run | the learnings entry. A `plugin-defect` result writes the record in its `error-report` route first |

The proposal that caused a halt has no `held` entry:

- After a file that `/sdlc:harden` could not restore, the proposal is in neither
  `applied[]` nor `held[]`, and `skipped` counts it. The record does not show
  the file. The `NOT restored: <file>` line in the output of `/sdlc:harden` is the
  only record of it.
- After a failed Copilot mirror, the proposal is in `applied[]`, because its
  own edit was written and checked. The record does not show the mirror
  failure. The halt message in the output of `/sdlc:harden` is the only record of
  it.

The full rules are in Step 5e of
[`harden/SKILL.md`](../../../plugins/sdlc/skills/harden/SKILL.md).

## A record that stays started

A record can stay `started`. These are the causes:

| Cause | Record that stays | What happens next |
|---|---|---|
| The session ends between step 5-pre and step 5e | the `started` record | Nothing runs. On a resume, `/sdlc:ship` does not run the cluster again. The harden step of `/sdlc:ship` commits the leftover edits in its own commit, not the pipeline `commit` step. |
| The step 5e call fails (for example, a field is not valid, or a list is over its limit) | the `started` record | `/sdlc:harden` prints a warning line and goes on as the table above says. |

The dashboard shows a `started` record as "interrupted" only when the harden
step of the ship run no longer runs. While the harden step is `in_progress` and
the ship run is not complete, the record is not interrupted. So a session that
ends in the middle of the harden step shows a stalled pipeline, and the record
keeps `interrupted: false` until the harden step ends (see [the dashboard
page](../../dashboard.md)).

## Runs with no record

A run that stops before step 5-pre leaves no record, and the dashboard shows no
run. These stops include:

- an error in the arguments, for example `--failure-text` together with
  `--from-issue`;
- an error from the load of the failure, for example a `config.toml` that
  cannot be read or an invalid `[harden.instructions]` table (see
  [Custom instructions](./custom-instructions.md));
- an orchestrator response that is missing or is not valid JSON;
- a `plugin-defect` result that breaks the contract of the orchestrator.

For the last two, `/sdlc:harden` shows the raw response, removes the manifest
and stops. `/sdlc:ship` records each failed invocation as a decision and goes
on to the next cluster.
