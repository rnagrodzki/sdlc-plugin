# Spec Delta

## ADDED Requirements

### Requirement: init stores planned task names
The `init` action SHALL write `plannedTasks` with one `{id, name}` for each `### Task N:` heading of a readable plan file, in plan order. Headings inside code fences SHALL be ignored. Without a readable plan file, `init` SHALL write no `plannedTasks` key.

#### Scenario: Plan with three tasks
- **WHEN** `init` gets a `planPath` whose plan has 3 task headings
- **THEN** the state has `plannedTasks` with ids `1`, `2`, `3` and their titles

#### Scenario: No plan path
- **WHEN** `init` gets no `planPath`
- **THEN** the state has no `plannedTasks` key

#### Scenario: Plan with no task headings
- **WHEN** the plan file has no task heading
- **THEN** the state has `plannedTasks: []`

### Requirement: First ledger check-in writes the review run meta
The first `ledger_checkin` of a run id SHALL create `.sdlc-v2/runs/ledger/<runId>/run.meta` with `branch`, `startedAt`, and `shipRunId` when a ship run on the branch has its `review` step `in_progress`. A later check-in SHALL not change the file. A write failure SHALL not fail the check-in.

#### Scenario: First check-in during a ship review
- **WHEN** the first `ledger_checkin` of run `review-2026-10-07T11-25-17Z` occurs
- **AND** the ship run on the branch has the `review` step `in_progress`
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
