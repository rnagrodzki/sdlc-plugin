# Spec Delta

## ADDED Requirements

### Requirement: commit-check action
The action `commit-check` SHALL stage with `git add -A -- ':!.sdlc-v2/'`, count staged paths, and store HEAD once as `commitBaseHead`. It SHALL require the `commit` step `in_progress`. A clean tree SHALL complete the step and return `stepCompleted: true`. A dirty tree SHALL change no step. Every outcome SHALL set `next`.

#### Scenario: Clean after execute
- **WHEN** execute made 5 wave commits, the tree is clean, and HEAD equals `commitBaseHead`
- **THEN** the commit step is `completed` with result `nothing to commit: execute committed 5 wave commit(s)`

#### Scenario: Commit landed before a stop
- **WHEN** the tree is clean and HEAD is `3f2a9c1`, not `commitBaseHead`
- **THEN** the commit side effect is journaled for `3f2a9c1`
- **AND** the commit step is `completed` with result `committed 3f2a9c1`

#### Scenario: Dirty tree
- **WHEN** 3 paths are staged
- **THEN** the result has `clean: false`, `stagedCount: 3`, `stepCompleted: false`

#### Scenario: Wrong step state
- **WHEN** the `commit` step is `pending`
- **THEN** the call returns a DomainError with the Suggestion `Call begin-step for commit first.`

#### Scenario: Git error
- **WHEN** `git add` fails
- **THEN** the call returns an InfraError with a Suggestion

### Requirement: Review wave table in the ship report
The ship report SHALL have a `## Review waves` section right after `## Review ledger`, with one totals line and one row for each planned dimension: wave, name, status, findings, and duration. Row statuses SHALL be `done`, `skipped-stalled`, `skipped-missing`, `still-running`, `never-started`, or `in-progress`.

#### Scenario: Partial review
- **WHEN** the review planned 23 dimensions in 3 waves and ran 8 in wave 1
- **THEN** the totals line is `Waves planned 3 · run 1 · dimensions planned 23 · run 8 · never started 15`

#### Scenario: Unstopped worker
- **WHEN** a dimension has stop reason `unstopped`
- **THEN** its row status is `still-running`

#### Scenario: No ledger
- **WHEN** no review ledger has the ship run id
- **THEN** the section says `Review waves: no review ledger found for this run.`
