# Spec Delta

## ADDED Requirements

### Requirement: evidence_record writer start time
The `evidence_record` action SHALL set `updatedAt` (UTC, RFC 3339) on every write. A write to a writer file that has no `startedAt` SHALL set `startedAt` to that `updatedAt`; every later write SHALL keep it. The `record` output SHALL echo `startedAt` and `updatedAt`. The `evidence_digest` writers table does not change.

#### Scenario: First write
- **WHEN** `evidence_record` writes writer `lane-style-compliance-r1` for the first time
- **THEN** the writer file has `startedAt` equal to `updatedAt`

#### Scenario: Later write
- **WHEN** `evidence_record` writes the same writer again later
- **THEN** `startedAt` keeps its first value
- **AND** `updatedAt` holds the time of the later write
- **AND** `record` echoes that `startedAt` and `updatedAt`

#### Scenario: File from before the change
- **WHEN** a writer file has no `startedAt` and `evidence_record` writes that writer
- **THEN** the call succeeds and the file has `startedAt` equal to `updatedAt`, which is later than the writer's real start

#### Scenario: Unreadable file replaced
- **WHEN** a writer file cannot be decoded (for example its `startedAt` is not a string) and `evidence_record` writes that writer
- **THEN** the call replaces the file and sets a new `startedAt` equal to `updatedAt`
