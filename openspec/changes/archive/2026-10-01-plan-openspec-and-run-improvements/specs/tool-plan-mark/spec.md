# Spec Delta

## MODIFIED Requirements

### Requirement: Append-only structured markers
For `guardrailResults` and `criticalDecisions`, the tool SHALL append the array in `data` to a top-level state key of the same name and SHALL NOT change `planIntegrity`.

| Marker | Array read from | Entry shape |
|---|---|---|
| `guardrailResults` | `data.results` | `{id, status, detail}` |
| `criticalDecisions` | `data.decisions` | `{key, choice, rejected, reason, at}` |

- A missing or non-array payload appends nothing; the call still succeeds.
- `rejected` is an array of `{option, why}`; a missing `rejected` is stored as `[]`.
- `at` is set by the tool to the call time, RFC 3339 UTC; a caller value is ignored.

#### Scenario: Two guardrailResults calls
- **WHEN** `plan_mark({marker: "guardrailResults", data: {results: [A]}})` is followed by the same call with `[B]`
- **THEN** the state key `guardrailResults` is `[A, B]`
- **AND** `planIntegrity` is unchanged

#### Scenario: Decision with rejected alternatives
- **WHEN** `plan_mark({marker:"criticalDecisions", data:{decisions:[{key:"k", choice:"merge", rejected:[{option:"rebase", why:"rewrites SHAs"}], reason:"r"}]}})` is called
- **THEN** the stored entry has `rejected` `[{option:"rebase", why:"rewrites SHAs"}]` and an `at` timestamp

#### Scenario: Decision without rejected
- **WHEN** a `criticalDecisions` entry has no `rejected`
- **THEN** the stored entry has `rejected: []`

## ADDED Requirements

### Requirement: Plan run survives the done marker
After `planIntegrity.done` is set, the plan run state file and its `<runId>.evidence/` dir SHALL stay on disk until ship's `cleanup-pipeline` deletes them after the report, or until GC removes them by TTL. No Stop hook deletes them.

- A run with `planIntegrity.done` set is never selected as the active plan run.

#### Scenario: Stop after planning
- **WHEN** `plan_mark({marker:"done"})` is called and the session then stops
- **THEN** `.sdlc-v2/runs/plan-<slug>-<ts>.json` and `.sdlc-v2/runs/plan-<slug>-<ts>.evidence/` still exist

#### Scenario: Done run is not resumed
- **WHEN** the only plan run for the branch has `planIntegrity.done` set and `plan_prepare` runs
- **THEN** a new plan run is created
