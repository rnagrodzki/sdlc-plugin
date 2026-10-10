# Spec Delta

## MODIFIED Requirements

### Requirement: healing_record
The `healing_record` action SHALL validate one record of `detail.kind` `review-total`, `fixed`, `hardened`, or `fix-progress`, and SHALL write it to the live run's `healing` object.

| `detail.kind` | Required fields | Write rule |
|---|---|---|
| `review-total` | `total`, `dimensions` (non-negative integers) | Replaces `healing.reviewTotal`. |
| `fixed` | `origin` (`local-review` \| `pr-comment`), `severity` (review severity, stored lowercase), `file`, `title`; optional integer `line` | Appends to `healing.fixed`; a record with the same `(origin, file, line, title)` is a duplicate. |
| `hardened` | `phase` (`started` \| `done`), `trigger`, `classification`, `applied` (array of `{surface, action, targetFile}`, may be empty), `skipped` (non-negative integer) | Upserts `healing.hardened` by `trigger`: `done` replaces a stored `started`; any other repeat is a duplicate. |
| `fix-progress` | `origin`, `severity`, `file`, `title`, `status` (`queued` \| `fixing` \| `fixed` \| `failed` \| `deferred`); optional integer `line` | Upserts `healing.fixProgress` on `(origin, file, line, title)`: keeps `firstAt`, sets `updatedAt`; a `deferred` status keeps a stored `failed`; at most 200 records. |

- `applied[].surface` is one of `plan-guardrails`, `execute-guardrails`, `review-dimensions`, `copilot-instructions`, `error-report-skill`, `skill-recommendation`.
- Each `review-total`, `fixed`, and `hardened` record gets `recordedAt`. A `fix-progress` record gets `firstAt` and `updatedAt`.
- A `fix-progress` write never changes `healing.fixed`.
- Input is validated before the state lookup.
- With no state file, or a state with `pipelineCompletedAt` set, the call succeeds and writes nothing.

| Output field | Meaning |
|---|---|
| `summary` | `healing_record <kind>: <narration>`; narration is `recorded`, `replaced started record`, `already recorded — no change`, `kept failed`, or `no live ship run on this branch — healing not recorded`. |
| `kind` | The kind. |
| `written` | `true` only when this call changed the state file. |
| `record` | The validated record as persisted, `recordedAt` included. |
| `next` | Kind `fix-progress` only: the next instruction for the fix pass, with id `continue-fix-pass`. |

| Condition | Class | Message (short) |
|---|---|---|
| `kind` missing | `DomainError` | `healing_record: detail.kind is required — accepted values are review-total \| fixed \| hardened \| fix-progress` |
| Unknown `kind`, `origin`, `severity`, `phase`, or `status` | `DomainError` | `healing_record: detail.<field> "<v>" is not a recognised ... — accepted values are ...` |
| Unknown `applied[].surface` | `DomainError` | `healing_record: detail.applied[<i>].surface "<s>" is not a harden surface id — accepted values are ...` |
| Required field missing | `DomainError` | `healing_record: detail.<field> is required for kind "<kind>"` |
| Integer field negative or fractional | `DomainError` | `healing_record: detail.<field> must be a non-negative integer, got ...` |
| New `fix-progress` key when 200 records exist | `DomainError` | `healing_record: data.healing.fixProgress holds 200 records — the cap for one run` |
| Stored `healing.fixProgress` is not a list | `DataError` | `healing_record: data.healing.fixProgress is not a list — the ship state is damaged` |

#### Scenario: Fixed duplicate
- **WHEN** the same `fixed` record is sent twice
- **THEN** the second response has `written:false` and narration `already recorded — no change`

#### Scenario: Hardened done replaces started
- **WHEN** a `started` record exists for trigger `T` and a `done` record for `T` is sent
- **THEN** narration is `replaced started record` and `healing.hardened` has one entry for `T`

#### Scenario: No live run
- **WHEN** the state has `pipelineCompletedAt` set
- **THEN** the call succeeds with `written:false` and narration `no live ship run on this branch — healing not recorded`

#### Scenario: Fix progress status change
- **WHEN** a `fix-progress` record with status `queued` exists and the same key is sent with status `fixing`
- **THEN** `healing.fixProgress` has one record for the key with status `fixing`
- **AND** its `firstAt` does not change

#### Scenario: Deferred keeps failed
- **WHEN** a `fix-progress` record has status `failed` and the same key is sent with status `deferred`
- **THEN** the response has `written:false` and narration `kept failed`

#### Scenario: Cap reached
- **WHEN** `healing.fixProgress` holds 200 records and a new key is sent
- **THEN** the tool returns a `DomainError` and writes nothing
