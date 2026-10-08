# Spec Delta

## MODIFIED Requirements

### Requirement: Registration and annotations
The tool SHALL be registered as `review_prepare` with the title "Prepare code review payload" and the annotations below.

| Annotation | Value |
|---|---|
| `ReadOnly` | `false` (writes `run.meta`) |
| `Destructive` | `false` |
| `Idempotent` | `false` (each call mints a new `run_id`) |
| `OpenWorld` | `true` (the PR lookup calls the GitHub API through `gh`) |

#### Scenario: Client lists tools
- **WHEN** an MCP client lists the server's tools
- **THEN** `review_prepare` is present with `ReadOnly: false`, `Destructive: false`, `Idempotent: false`, `OpenWorld: true`

## ADDED Requirements

### Requirement: Run ID and worker IDs
In manifest mode with at least one wave, the manifest SHALL have `run_id` `review-` plus the timestamp with each character outside `[A-Za-z0-9_-]` replaced by `-`. Each wave dimension SHALL have `worker_id`: the lower-case name with each run of other characters replaced by one `-`.

#### Scenario: Run ID
- **WHEN** the manifest timestamp is `2026-10-08T11:09:18Z`
- **THEN** `run_id` is `review-2026-10-08T11-09-18Z`

#### Scenario: Worker ID
- **WHEN** a dimension is named `Security Review`
- **THEN** its `worker_id` is `security-review`

### Requirement: Review run plan write
In manifest mode with at least one wave and no `dryRun`, the tool SHALL write `.sdlc-v2/runs/ledger/<run_id>/run.meta` with `branch`, `startedAt`, `shipRunId` (when the ship review step is `in_progress`), `waves`, and `dimensions` `[{name, workerId, wave}]`. A dry run or zero waves SHALL write no file and return `run_id` `""`. A write failure SHALL return an InfraError with a Suggestion.

#### Scenario: Normal run
- **WHEN** `review_prepare` plans 3 dimensions in 2 waves
- **THEN** `run.meta` lists 3 dimensions with waves `1`, `1`, `2`

#### Scenario: Dry run
- **WHEN** `review_prepare` gets `dryRun: true`
- **THEN** no `run.meta` exists and `run_id` is `""`

#### Scenario: Zero waves
- **WHEN** no dimension matches the diff
- **THEN** no `run.meta` exists and `run_id` is `""`

#### Scenario: Save mode wins
- **WHEN** `saveReview` and `dryRun` are both `true`
- **THEN** the tool runs save mode
