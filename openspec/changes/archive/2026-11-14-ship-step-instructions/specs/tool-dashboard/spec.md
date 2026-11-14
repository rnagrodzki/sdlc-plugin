# Spec Delta

## ADDED Requirements

### Requirement: Instruction rows in the snapshot
The snapshot SHALL put each ship record under the step of its key, the commit-fixes records under received-review, and the `ship` records and standalone execute records in `endInstructions` with `endLabel`. A run with no record SHALL have neither field.

| Field | Value |
|---|---|
| `steps[].instructions` | key, number, source, text, status, outcome |
| `endInstructions` | same shape |
| `endLabel` | `end of run (ship)` or `end of run (execute)` |

#### Scenario: Status change
- **WHEN** a record changes from `running` to `done` in the state file
- **THEN** the next snapshot shows `done`

#### Scenario: Damaged key
- **WHEN** the `instructions` key is not a list
- **THEN** the run has no instruction field
