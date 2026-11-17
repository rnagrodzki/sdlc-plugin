# Spec Delta

## ADDED Requirements

### Requirement: Ship state records the thread id
A new ship state file SHALL hold `t3ThreadId` when the server process runs in a T3 Code thread.

#### Scenario: New ship run in T3 Code
- **WHEN** a ship run starts with `T3CODE_THREAD_ID` `abc-1`
- **THEN** the state file has `"t3ThreadId": "abc-1"`

#### Scenario: Invalid thread id
- **WHEN** `T3CODE_THREAD_ID` has a character outside `A-Z a-z 0-9 . _ -` or more than 128 characters
- **THEN** the state file has no `t3ThreadId` key

### Requirement: Read returns the run name
The `ship_state` `read` action SHALL return `runId`, the state file name without `.json`.

#### Scenario: Read after a compaction
- **WHEN** `ship_state` `read` runs for the branch
- **THEN** the output has `runId` and every state key, `t3ThreadId` included
