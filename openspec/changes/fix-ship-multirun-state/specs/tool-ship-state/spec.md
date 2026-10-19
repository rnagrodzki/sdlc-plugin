# Spec Delta

## MODIFIED Requirements

### Requirement: healing_record
The `healing_record` action SHALL validate one record of `detail.kind` `review-total`, `fixed`, `hardened`, or `fix-progress`, and SHALL write it to the live run's `healing` object.

| `detail.kind` | Required fields | Write rule |
|---|---|---|
| `review-total` | `total`, `dimensions` (non-negative integers) | Replaces `healing.reviewTotal`. |
| `fixed` | `origin` (`local-review` \| `pr-comment`), `severity` (review severity, stored lowercase), `file`, `title`; optional integer `line` (>= 1) | Appends to `healing.fixed`; a record with the same `(origin, file, line, title)` is a duplicate. Also sets the matching `healing.fixProgress` record to `fixed`, also on a duplicate. Match: the same `(origin, file, line, title)`, else the only record with the same file, line and title, else the only `queued` or `fixing` record with the same file and line. It adds no `fixProgress` record. A `fixProgress` value that is not a list does not fail the call: the `fixed` record is written, no `fixProgress` record changes, and the response carries a warning. |
| `hardened` | `phase` (`started` \| `done`), `trigger`, `classification`, `applied` (array of `{surface, action, targetFile}`, may be empty), `skipped` (non-negative integer) | Upserts `healing.hardened` by `trigger`: `done` replaces a stored `started`; any other repeat is a duplicate. |
| `fix-progress` | The `fixed` fields plus `status` (`queued` \| `fixing` \| `fixed` \| `failed` \| `deferred`) | Upserts `healing.fixProgress` on `(origin, file, line, title)`: the same status changes nothing; a stored final status (`fixed`, `failed`, `deferred`) is kept against `queued` or `fixing`; a `deferred` status keeps a stored `failed`; any other change replaces the record, keeps `firstAt` and `deferredId`, and sets `updatedAt`; at most 200 records. |

- `applied[].surface` is one of `plan-guardrails`, `execute-guardrails`, `review-dimensions`, `copilot-instructions`, `error-report-skill`, `skill-recommendation`.
- Each `review-total`, `fixed`, and `hardened` record gets `recordedAt`. A `fix-progress` record gets `firstAt` and `updatedAt`.
- A `fix-progress` write never changes `healing.fixed`.
- A `fixed` write that sets a `fixProgress` record keeps its `firstAt` and sets `updatedAt` to the `recordedAt` of the call.
- Input is validated before the state lookup.
- With no state file, or a state with `pipelineCompletedAt` set, the call succeeds and writes nothing.

| Output field | Meaning |
|---|---|
| `summary` | `healing_record <kind>: <narration>`; narration is `recorded`, `replaced started record`, `already recorded — no change`, `fix-progress row set to fixed`, `kept <status>`, or `no live ship run on this branch — healing not recorded`. |
| `kind` | The kind. |
| `written` | `true` only when this call changed the state file. |
| `record` | The record as stored after the call: `recordedAt` for `review-total`, `fixed` and `hardened`; `status`, `firstAt` and `updatedAt` for `fix-progress`. On a no-op call, and on a repeat `fixed` call that only sets its `fixProgress` row, it is the stored entry. With no live run it is the validated incoming record. |
| `fixRow` | Kind `fixed` only, when the call changed the status of a `fixProgress` record: `{status, prior, updatedAt}`. |
| `warnings` | Kind `fixed` only, when `healing.fixProgress` is not a list: one warning that names the state file. The warning is also at the end of `summary` and `next`. |
| `next` | Every kind returns it. Kinds `fix-progress` and `fixed` use id `continue-fix-pass`; with `detail.deferredId`, the instruction of kind `fixed` names the deferred item; when kind `fixed` changed a row, the instruction names the prior status. Kind `review-total` uses id `continue-after-review-total`. Kind `hardened` uses id `continue-harden`: phase `started` asks for the `done` record, and phase `done` ends the harden run. |

| Condition | Class | Message (short) |
|---|---|---|
| `kind` missing | `DomainError` | `healing_record: detail.kind is required — accepted values are review-total \| fixed \| hardened \| fix-progress` |
| Unknown `kind`, `origin`, `severity`, `phase`, or `status` | `DomainError` | `healing_record: detail.<field> "<v>" is not a recognised ... — accepted values are ...` |
| Unknown `applied[].surface` | `DomainError` | `healing_record: detail.applied[<i>].surface "<s>" is not a harden surface id — accepted values are ...` |
| Required field missing | `DomainError` | `healing_record: detail.<field> is required for kind "<kind>"` |
| Integer field negative or fractional | `DomainError` | `healing_record: detail.<field> must be a non-negative integer, got ...` |
| New `fix-progress` key when 200 records exist | `DomainError` | `healing_record: data.healing.fixProgress holds 200 records — the cap for one run` |
| `line` below 1 | `DomainError` | `healing_record: detail.line must be >= 1, got <n>` |
| Stored `healing.fixed`, `healing.hardened` or `healing.fixProgress` is not a list, or `healing` is not an object (except `healing.fixProgress` on kind `fixed`, which gives a warning) | `DataError` | `healing_record: data.healing.<key> in <state file> is not a list — the ship state is damaged` |

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
- **AND** `record` is the stored record with status `failed`

#### Scenario: Final status kept against a late queued
- **WHEN** a `fix-progress` record has status `fixed` and the same key is sent with status `queued`
- **THEN** the response has `written:false`, narration `kept fixed`, and `record.status` `fixed`

#### Scenario: Cap reached
- **WHEN** `healing.fixProgress` holds 200 records and a new key is sent
- **THEN** the tool returns a `DomainError` and writes nothing

#### Scenario: Fixed call sets the fix record
- **WHEN** a `fix-progress` record with status `queued` exists for key `K`
- **AND** a `fixed` record with key `K` is sent
- **THEN** the `fix-progress` record for `K` has status `fixed`
- **AND** its `firstAt` does not change

#### Scenario: Fixed call with a changed title
- **WHEN** the only `queued` `fix-progress` record on `internal/foo.go` line 12 has title `unchecked error`
- **AND** a `fixed` record for `internal/foo.go` line 12 with title `unchecked err` is sent
- **THEN** that `fix-progress` record has status `fixed`
- **AND** `healing.fixProgress` has the same number of records

#### Scenario: Two open records on one line
- **WHEN** two `queued` `fix-progress` records on one file and line have other titles than the `fixed` record
- **THEN** no `fix-progress` record changes

#### Scenario: Repeat fixed call repairs the record
- **WHEN** `healing.fixed` already holds the `fixed` record and its `fix-progress` record has status `queued`
- **AND** the same `fixed` record is sent again
- **THEN** the response has `written:true` and narration `fix-progress row set to fixed`
- **AND** `record` is the stored `fixed` record
- **AND** `fixRow` is `{status:"fixed", prior:"queued", updatedAt:<call time>}`

#### Scenario: Fixed call with a damaged fix-progress list
- **WHEN** `healing.fixProgress` is not a list
- **AND** a `fixed` record is sent, with or without `deferredId`
- **THEN** the call succeeds and writes the `fixed` record
- **AND** `healing.fixProgress` is unchanged
- **AND** `summary`, `next` and `warnings` hold `Fix-progress row not updated: data.healing.fixProgress in <path> is not a list — the ship state is damaged. Tell the user to repair the ship state file <path> (set the damaged key to {} or [], or remove it).`
- **AND** with `deferredId` the `deferred.json` row is resolved

#### Scenario: Next for each kind
- **WHEN** a `review-total` record is stored
- **THEN** the response has `next` id `continue-after-review-total`
- **AND** a stored `hardened` record gives `next` id `continue-harden`, with an instruction that differs for phase `started` and phase `done`
- **AND** a stored `fixed` record with no `deferredId` gives `next` id `continue-fix-pass`

### Requirement: init (legacy)
The `init` action SHALL create a new ship state file with the fixed legacy scaffold and delete the other ship state files for the same branch slug that are not kept. It SHALL keep a completed run that is not a dry-run, and a run that is not a dry-run whose non-empty `sessionId` differs from the non-empty `sessionId` of the call.

- Seeded: `version:1`, `startedAt`, `branch`, `worktree`, `sessionId`, `flags` (`detail.flags` or `{}`), `decisions:[]`, `deferredFindings:[]`.
- `steps`: `execute`, `commit`, `review`, `received-review` (`condition:"if critical/high findings"`), `commit-fixes` (`condition:"if received-review made changes"`), `pr`; all `pending`, no `kind`.
- Output: `{filePath, prunedOrphans, worktree}`.
- `prunedOrphans` lists exactly the files that the write deleted; a failed delete is not listed. GC deletes a kept file later.

#### Scenario: Legacy scaffold
- **WHEN** the call passes `action:"init"` with `detail.branch:"feat/x"`
- **THEN** the new file's `steps` has 6 entries and `received-review` carries a `condition`

#### Scenario: Legacy init keeps a completed run
- **WHEN** an older `ship-<same-slug>-*.json` file has `pipelineStatus` `completed`
- **AND** the call passes `action:"init"` for the same branch
- **THEN** the older file is kept
- **AND** `prunedOrphans` does not list it

## ADDED Requirements

### Requirement: defer sets the fix-progress record
After a good `deferred.json` write, the `defer` action SHALL set the matching `healing.fixProgress` record of the live run to `deferred` and SHALL copy the `deferredId` of the run entry onto it. When the `deferred.json` write fails, it SHALL NOT change the record. Match: the only record with the same file, line and title, else the only `queued` or `fixing` record with the same file and line. A stored `failed` record keeps status `failed`.

#### Scenario: Defer sets the record
- **WHEN** a `queued` `fix-progress` record exists on `internal/foo.go` line 12 with title `unchecked error`
- **AND** `defer` is called for the same file, line and title
- **THEN** that record has status `deferred` and `updatedAt` set to the call time
- **AND** that record has the `deferredId` of the new run entry

#### Scenario: Defer keeps failed
- **WHEN** a `failed` `fix-progress` record matches the deferred finding
- **THEN** that record keeps status `failed` and its `updatedAt`
- **AND** that record gets the `deferredId`

#### Scenario: Defer after a fixed record
- **WHEN** a `fixed` `fix-progress` record matches the deferred finding
- **THEN** that record has status `deferred` and the `deferredId`

#### Scenario: No matching record
- **WHEN** no `fix-progress` record matches the deferred finding
- **THEN** the `defer` result is the same as with no `healing` key

#### Scenario: Failed deferred.json write
- **WHEN** the `deferred.json` write fails
- **THEN** the matching record is unchanged
- **AND** `summary` and `next` hold no `Fix-progress row` note

### Requirement: defer reports the fix-progress change
The `defer` action SHALL add one note to the end of `summary` and of `next` when the call changes a `fix-progress` record.

| Change | Note |
|---|---|
| The status changed from `<status>` to `deferred` | `Fix-progress row set to deferred (was <status>).` |
| The status stayed `<status>` and the record only got the `deferredId` | `Fix-progress row kept <status> and got the deferred id.` |
| No record matched, or the call changed nothing | No note. |

#### Scenario: Note for a changed status
- **WHEN** a `queued` `fix-progress` record matches the deferred finding
- **THEN** `summary` and `next` end with `Fix-progress row set to deferred (was queued).`

#### Scenario: Note for a failed record
- **WHEN** a `failed` `fix-progress` record matches the deferred finding
- **THEN** `summary` and `next` end with `Fix-progress row kept failed and got the deferred id.`

#### Scenario: Second defer reaches a deferred record
- **WHEN** a `deferred` `fix-progress` record matches the deferred finding
- **AND** the call gives it another `deferredId`
- **THEN** that record keeps status `deferred` and its `updatedAt`
- **AND** that record gets the new `deferredId`
- **AND** `summary` and `next` end with `Fix-progress row kept deferred and got the deferred id.`

#### Scenario: No note without a change
- **WHEN** no `fix-progress` record matches, or the call changes no field
- **THEN** `summary` and `next` hold no `Fix-progress row` note

### Requirement: defer warns on damaged healing data
The `defer` action SHALL NOT fail for a damaged `healing` object. It SHALL leave every `fix-progress` record unchanged and SHALL add one warning that names the damage to `summary` and to `next`.

#### Scenario: Healing is not an object
- **WHEN** `data.healing` is not an object
- **THEN** the `defer` call succeeds
- **AND** `summary` and `next` hold `Fix-progress row not updated: data.healing in <path> is not an object — the ship state is damaged. Tell the user to repair the ship state file <path> (set the damaged key to {} or [], or remove it).`

#### Scenario: Fix-progress list is damaged
- **WHEN** `data.healing.fixProgress` is not a list
- **THEN** the `defer` call succeeds
- **AND** `summary` and `next` hold `Fix-progress row not updated: data.healing.fixProgress in <path> is not a list — the ship state is damaged. Tell the user to repair the ship state file <path> (set the damaged key to {} or [], or remove it).`
