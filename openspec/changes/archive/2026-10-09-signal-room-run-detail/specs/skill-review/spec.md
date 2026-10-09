# Spec Delta

## ADDED Requirements

### Requirement: Run ID and worker IDs from the manifest
The skill SHALL use `manifest.run_id` as the ledger run ID and `dimension.worker_id` as each worker ID. It SHALL keep no slug or sanitize rule of its own. It SHALL forward `--dry-run` to `review_prepare` as `dryRun: true`.

#### Scenario: Normal run
- **WHEN** the manifest has `run_id` `review-2026-10-08T11-09-18Z`
- **THEN** every `ledger_checkin` uses that run ID

#### Scenario: Dry run
- **WHEN** the user runs the review with `--dry-run`
- **THEN** the skill calls `review_prepare` with `dryRun: true`
- **AND** no ledger folder exists

### Requirement: Stopped workers are recorded
For each worker that the skill stops, the skill SHALL call `execute_state` `ledger_skip` with reason `stalled` or `missing`. When `TaskStop` fails or no task ID exists, the reason SHALL be `unstopped`, and the comment SHALL name the worker as possibly still running. A failed `ledger_skip` SHALL not stop the review.

#### Scenario: Stalled worker stopped
- **WHEN** a worker stalls twice and `TaskStop` succeeds
- **THEN** the skill calls `ledger_skip` with reason `stalled`

#### Scenario: Nested under ship
- **WHEN** a worker stalls twice and `TaskStop` fails
- **THEN** the skill calls `ledger_skip` with reason `unstopped`
- **AND** the review comment names the worker as possibly still running
