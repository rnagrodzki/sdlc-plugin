# Spec Delta

## MODIFIED Requirements

### Requirement: Staging before commit dispatches
The skill SHALL leave staging for `commit` to `ship_state` `commit-check`, and SHALL run `git add -A -- ':!.sdlc-v2/'` right before the `commit-fixes` dispatch.

- A failed `git add` stops the pipeline.
- The harden commit stages only `dirtySurfaces` paths with `git add -- <path>`; never `git add -A`.

#### Scenario: New file from execute
- **WHEN** `execute` creates a new untracked test file
- **THEN** `commit-check` stages it before the skill dispatches `commit`

#### Scenario: Fixes after review
- **WHEN** `received-review` changes files
- **THEN** the skill stages them before the `commit-fixes` dispatch

## ADDED Requirements

### Requirement: Commit step route
After `begin-step` for `commit` returns no `alreadyDone`, the skill SHALL call `ship_state` `commit-check`. With `stepCompleted: true`, it SHALL run no commit agent, no side-effect check, and no `complete-step`, and SHALL update the todo list from the returned `todos`. With `stepCompleted: false`, it SHALL dispatch the commit agent, record the side effect, and call `complete-step`.

#### Scenario: Clean tree
- **WHEN** `commit-check` returns `stepCompleted: true`
- **THEN** no commit agent runs
- **AND** the loop goes to the next step

#### Scenario: Dirty tree
- **WHEN** `commit-check` returns `stepCompleted: false`
- **THEN** the skill dispatches the commit agent

### Requirement: Review gaps are recorded
Before the review `complete-step`, the skill SHALL record with `decide` each dimension that review reports as never started, stalled, missing, or still running.

#### Scenario: Never-started dimension
- **WHEN** review reports `docs-review` as never started in wave 2
- **THEN** the skill records `decide` with text `docs-review never started (wave 2)`

### Requirement: Saved OpenSpec change missing on disk
When `verify-openspec` or `archive-openspec` finds the named change missing on disk, the failure message SHALL tell the user to merge the OpenSpec PR first when the plan has an `**OpenSpec-Saved:**` line.

#### Scenario: Unmerged OpenSpec PR
- **WHEN** the plan has `**OpenSpec-Saved:**` and `openspec/changes/<n>/` is missing
- **THEN** `verify-openspec` fails with a message that says to merge the OpenSpec PR (branch `openspec/<n>`) first
