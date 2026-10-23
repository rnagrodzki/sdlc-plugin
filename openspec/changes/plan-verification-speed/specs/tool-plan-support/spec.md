# Spec Delta

## ADDED Requirements

### Requirement: evidence_record writer start time
The `evidence_record` action SHALL set `startedAt` (UTC, RFC 3339) on the first write of a writer file and SHALL keep it on every later write, while `updatedAt` moves on every write.

- A writer file with no `startedAt` (the form before this change) gets `startedAt` equal to `updatedAt` at its next write, with no error.
- A write that replaces an unreadable file, or a file with a bad `startedAt`, sets a new `startedAt`.
- The `evidence_digest` writers table does not change.

#### Scenario: First write
- **WHEN** `evidence_record` writes writer `lane-style-compliance-r1` for the first time
- **THEN** the writer file has `startedAt` equal to `updatedAt`

#### Scenario: Later write
- **WHEN** `evidence_record` writes the same writer again later
- **THEN** `startedAt` keeps its first value
- **AND** `updatedAt` holds the time of the later write

#### Scenario: File from before the change
- **WHEN** a writer file has no `startedAt` and `evidence_record` writes that writer
- **THEN** the call succeeds and the file has `startedAt` equal to `updatedAt`
