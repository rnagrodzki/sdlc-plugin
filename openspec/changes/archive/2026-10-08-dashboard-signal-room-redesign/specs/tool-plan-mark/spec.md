# Spec Delta

## MODIFIED Requirements

### Requirement: Registration and annotations
The tool SHALL be registered as `plan_mark`, marked "INTERNAL — called by sdlc skills only", with annotations `ReadOnly: true`, `Idempotent: false`, `OpenWorld: false`.

| Annotation | Value |
|---|---|
| Title | `Record plan progress marker` |
| ReadOnly | `true` |
| Idempotent | `false` |
| OpenWorld | `false` |

#### Scenario: Tool is listed with its annotations
- **WHEN** an MCP client lists the server's tools
- **THEN** `plan_mark` is listed with title `Record plan progress marker`
- **AND** its `marker` input declares all 9 marker names as a JSON-schema enum

### Requirement: Input and output fields
The tool SHALL accept the input fields below and SHALL return the output fields below on success.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `marker` | string enum | yes | plain text | One of `plan-file`, `skillInvoked`, `guardrailsEvaluated`, `critiqueRan`, `done`, `guardrailResults`, `criticalDecisions`, `checkpoint`, `review-round` |
| `path` | string | only for `plan-file` | plain text | Plan document path to record |
| `data` | object | only for `checkpoint` and `review-round` | JSON | Payload for `guardrailResults`, `criticalDecisions`, `checkpoint` and `review-round`; ignored otherwise |

| Field | Meaning |
|---|---|
| `ok` | `true` on success |
| `marker` | The marker that was written |
| `path` | Path of the plan state file that was written |
| `next` | Set only for `checkpoint` |
| `warnings` | Set only for `done` when the history append failed |

#### Scenario: Successful timestamp marker
- **WHEN** `plan_mark({marker: "critiqueRan"})` succeeds
- **THEN** the output has `ok: true`, `marker: "critiqueRan"` and `path` set to the plan state file
- **AND** `next` is empty

## ADDED Requirements

### Requirement: Review-round marker
The `review-round` marker SHALL store one row of `reviewRounds` in the plan state with `round`, `mergedStatus`, `found`, `fixed`, and `lenses`. A row with the same `round` SHALL be replaced. Rows SHALL stay sorted by `round`. The marker SHALL return no `next`.

#### Scenario: First call for a round
- **WHEN** `plan_mark({marker: "review-round", data: {round: 1, mergedStatus: "Approved", found: 0, fixed: 0, lenses: [{name: "all", verdict: "Approved"}]}})` succeeds
- **THEN** the plan state has one `reviewRounds` row with `round: 1`
- **AND** the output has no `next`

#### Scenario: Replayed round
- **WHEN** `reviewRounds` has a row with `round: 2, fixed: 0`
- **AND** a `review-round` call sends `round: 2, fixed: 2`
- **THEN** `reviewRounds` has one row with `round: 2` and `fixed: 2`

### Requirement: Review-round data errors
The tool SHALL reject an invalid `review-round` payload with a `DomainError` that has a `Suggestion`, and SHALL not change the plan state file.

#### Scenario: Bad status
- **WHEN** a `review-round` call sends `mergedStatus: "ok"`
- **THEN** the tool returns a `DomainError` whose Suggestion contains `Approved` and `Issues Found`
- **AND** the plan state file bytes do not change

#### Scenario: Unknown key
- **WHEN** a `review-round` call sends the key `at`
- **THEN** the `DomainError` names `at`
- **AND** its Suggestion lists `round, mergedStatus, found, fixed, lenses`

#### Scenario: Bad round number
- **WHEN** a `review-round` call sends `round: 0`
- **THEN** the tool returns a `DomainError`

#### Scenario: Bad counts
- **WHEN** a `review-round` call sends `found: -1`
- **THEN** the tool returns a `DomainError`

#### Scenario: Too many lenses
- **WHEN** a `review-round` call sends 33 lenses
- **THEN** the tool returns a `DomainError`

#### Scenario: Too many rounds
- **WHEN** the plan state has 20 rounds and a call sends `round: 21`
- **THEN** the tool returns a `DomainError`
