# Spec Delta

## MODIFIED Requirements

### Requirement: healing_record
The `healing_record` action SHALL validate one record of `detail.kind` `review-total`, `fixed`, `hardened`, or `fix-progress`, and SHALL write it to the live run's `healing` object.

| `detail.kind` | Required fields | Write rule |
|---|---|---|
| `review-total` | `total`, `dimensions` (non-negative integers) | Replaces `healing.reviewTotal`. |
| `fixed` | `origin` (`local-review` \| `pr-comment`), `severity` (review severity, stored lowercase), `file`, `title`; optional integer `line` (>= 1) | Appends to `healing.fixed`; a record with the same `(origin, file, line, title)` is a duplicate. Also sets the matching `healing.fixProgress` record to `fixed`, also on a duplicate. Match: the same `(origin, file, line, title)`, else the only record with the same file, line and title, else the only `queued` or `fixing` record with the same file and line. It adds no `fixProgress` record. A `fixProgress` value that is not a list does not fail the call: the `fixed` record is written, no `fixProgress` record changes, and the response carries a warning. |
| `hardened` | `phase` (`started` \| `done`), `trigger`, `classification`, `applied` (array of `{surface, action, targetFile}`, may be empty; each entry also takes optional string `rationale` and `patch`), `skipped` (non-negative integer); optional `held` (array of `{surface, action, targetFile, reason, deferredId}`, at most 100 entries, may be empty) | Upserts `healing.hardened` by `trigger`: `done` replaces a stored `started`; any other repeat is a duplicate. An empty `rationale` or `patch` stores no key. A `patch` over 1536 bytes is cut to at most 1536 bytes on a UTF-8 boundary, and the entry gets `patchCut: true`. An absent `held` stays absent; `held: []` is stored as `[]`. |
| `fix-progress` | The `fixed` fields plus `status` (`queued` \| `fixing` \| `fixed` \| `failed` \| `deferred`) | Upserts `healing.fixProgress` on `(origin, file, line, title)`: the same status changes nothing; a stored final status (`fixed`, `failed`, `deferred`) is kept against `queued` or `fixing`; a `deferred` status keeps a stored `failed`; any other change replaces the record, keeps `firstAt` and `deferredId`, and sets `updatedAt`; at most 200 records. |

- `applied[].surface` is one of `plan-guardrails`, `execute-guardrails`, `review-dimensions`, `copilot-instructions`, `error-report-skill`, `skill-recommendation`.
- `held[].surface` takes the same values as `applied[].surface`. `held[].reason` is one of `pending-stored`, `pending-not-stored`, `not-processed`.
- `held[].deferredId` is required for reason `pending-stored` and absent for the other reasons. Two entries can share one `deferredId`. The tool does not check that `held` lists every item of the harden summary, and does not check `deferredId` against the stored deferred items.
- The 1536-byte patch limit is fixed; no setting changes it. Only the tool sets `patchCut`.
- A `hardened` record without `rationale`, `patch` and `held` stays valid against the ship state schema.
- Each `review-total`, `fixed`, and `hardened` record gets `recordedAt`. A `fix-progress` record gets `firstAt` and `updatedAt`.
- A `fix-progress` write never changes `healing.fixed`.
- A `fixed` write that sets a `fixProgress` record keeps its `firstAt` and sets `updatedAt` to the `recordedAt` of the call.
- Input is validated before the state lookup. A DomainError of kind `hardened` writes nothing.
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
| `applied[].rationale` or `applied[].patch` is not a string | `DomainError` | names `detail.applied[<i>].<field>`; the Suggestion names the optional `rationale` and `patch` fields |
| `applied[].patchCut` sent by the caller | `DomainError` | `detail.applied[<i>].patchCut is set by the tool — remove it` |
| `held` is not a list, or a `held` entry is not an object or misses `surface`, `action` or `targetFile` | `DomainError` | names `detail.held` or `detail.held[<i>].<field>` |
| `held` has more than 100 entries | `DomainError` | `detail.held has <n> entries — the limit is 100`; Suggestion `Send at most 100 held entries, then retry ship_state healing_record.` |
| Unknown `held[].surface` or `held[].reason` | `DomainError` | names the field and lists the accepted values |
| `held[].deferredId` missing with `pending-stored`, present with another reason, empty, or not a string | `DomainError` | names the `deferredId` rule |
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

#### Scenario: Old hardened record form
- **WHEN** a `hardened` record is sent whose `applied` entries have only `surface`, `action` and `targetFile`, with no `held` key
- **THEN** the stored entries have no `rationale`, `patch` or `patchCut` key
- **AND** the stored record has no `held` key

#### Scenario: Empty rationale and patch
- **WHEN** an `applied` entry has `rationale: ""` and `patch: ""`
- **THEN** the stored entry has no `rationale` key and no `patch` key

#### Scenario: Empty held list
- **WHEN** a `hardened` record is sent with `held: []`
- **THEN** the stored record has `held: []`

#### Scenario: Patch at the limit
- **WHEN** an `applied` entry has a `patch` of exactly 1536 bytes
- **THEN** the stored `patch` is whole and the entry has no `patchCut` key

#### Scenario: Patch over the limit
- **WHEN** an `applied` entry has a `patch` of 1537 ASCII bytes
- **THEN** the stored `patch` has 1536 bytes
- **AND** the entry has `patchCut: true`

#### Scenario: Multi-byte character at the cut point
- **WHEN** a multi-byte character spans byte 1536 of a `patch`
- **THEN** the stored `patch` is valid UTF-8 and has at most 1536 bytes
- **AND** the entry has `patchCut: true`

#### Scenario: Caller sends patchCut
- **WHEN** an `applied` entry at index 0 has `patchCut: true`
- **THEN** the tool returns a `DomainError` with `detail.applied[0].patchCut is set by the tool — remove it`
- **AND** the state file does not change

#### Scenario: Held list at and over the limit
- **WHEN** a `hardened` record has 100 `held` entries
- **THEN** the record is stored with 100 `held` entries
- **AND** the same record with 101 `held` entries returns a `DomainError` with `detail.held has 101 entries — the limit is 100` and the Suggestion `Send at most 100 held entries, then retry ship_state healing_record.`

#### Scenario: Pending-stored entry with no deferred id
- **WHEN** a `held` entry has reason `pending-stored` and no `deferredId`
- **THEN** the tool returns a `DomainError` that names the `deferredId` rule
- **AND** the state file does not change

#### Scenario: Deferred id with another reason
- **WHEN** a `held` entry has reason `not-processed` and `deferredId` `harden-pending-plan`
- **THEN** the tool returns a `DomainError` that names the `deferredId` rule

#### Scenario: Two held entries share one deferred id
- **WHEN** two `held` entries have reason `pending-stored` and `deferredId` `harden-pending-dimensions`
- **THEN** the record is stored with both entries
