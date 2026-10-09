# Spec Delta

## MODIFIED Requirements

### Requirement: First ledger check-in writes the review run meta
When `run.meta` is absent, the first `ledger_checkin` of a run id SHALL create `.sdlc-v2/runs/ledger/<runId>/run.meta` with `branch`, `startedAt`, and `shipRunId` when a ship run on the branch has its `review` step `in_progress`. `review_prepare` normally writes the file first. A later check-in SHALL not change the file. A write failure SHALL not fail the check-in.

#### Scenario: First check-in during a ship review
- **WHEN** the first `ledger_checkin` of run `review-2026-10-07T11-25-17Z` occurs
- **AND** the ship run on the branch has the `review` step `in_progress`
- **AND** `run.meta` does not exist
- **THEN** `run.meta` holds the branch, the check-in time, and the ship run id

#### Scenario: Second check-in
- **WHEN** `run.meta` exists and another worker checks in
- **THEN** `run.meta` does not change

#### Scenario: Meta write fails
- **WHEN** `run.meta` cannot be written
- **THEN** the check-in succeeds
- **AND** the result has a `warnings` entry that names the `run.meta` path

#### Scenario: Branch lookup fails
- **WHEN** the branch of the work dir cannot be read
- **THEN** `run.meta` is written with `branch: ""`

#### Scenario: Written by review_prepare
- **WHEN** `review_prepare` wrote `run.meta` with `waves`
- **AND** the first worker checks in
- **THEN** `run.meta` does not change

## ADDED Requirements

### Requirement: ledger_skip records a stopped review worker
The action `ledger_skip` SHALL take `runId`, `workerId`, and `reason` (`stalled`, `missing`, or `unstopped`) and SHALL set `stopReason` of that dimension in `run.meta` with an atomic write. It SHALL not write any worker file; it MAY read the worker file to warn that the worker already checked out. The result SHALL echo `workerId`, the recorded `stopReason`, and the replaced `priorStopReason` when one was set. A missing `run.meta`, an unknown `workerId`, or a bad `reason` SHALL return a DomainError with a Suggestion.

#### Scenario: Missing worker
- **WHEN** `ledger_skip` gets `workerId` `docs-review` and `reason` `missing`
- **THEN** the `docs-review` entry of `run.meta` has `stopReason` `missing`
- **AND** `ledger_status` output does not change

#### Scenario: No run meta
- **WHEN** `run.meta` does not exist
- **THEN** the call returns a DomainError whose Suggestion says to pass the `run_id` from the `review_prepare` manifest of this review run, and that a dry run or a run with zero waves has no `run.meta`

#### Scenario: Unknown worker
- **WHEN** `workerId` is not in `run.meta` `dimensions`
- **THEN** the call returns a DomainError whose Suggestion lists the planned worker IDs

### Requirement: init stores planned waves
`init` SHALL take optional `plannedWavesJson`, a JSON-encoded array of `{number, taskIds}`, and SHALL store it as `plannedWaves`. Wave `0` SHALL hold the pre-wave tasks. A bad value SHALL return a DomainError with a Suggestion and write no state. Without the field, no key SHALL be stored.

#### Scenario: Two waves
- **WHEN** `init` gets `plannedWavesJson` `[{"number":1,"taskIds":["2","3"]}]`
- **THEN** the state has `plannedWaves` `[{number:1, taskIds:["2","3"]}]`

#### Scenario: Duplicate wave number
- **WHEN** `plannedWavesJson` has wave `1` twice
- **THEN** `init` returns a DomainError with the Suggestion `Give each wave number once.`

#### Scenario: Negative wave number
- **WHEN** a wave has `number` `-1`
- **THEN** `init` returns a DomainError and writes no state

#### Scenario: Empty wave
- **WHEN** a wave has empty `taskIds`
- **THEN** `init` returns a DomainError with the Suggestion `Leave out a wave that has no tasks.`

#### Scenario: Unknown task
- **WHEN** `plannedTaskIds` is `["1","2"]` and a wave holds task `9`
- **THEN** `init` returns a DomainError
