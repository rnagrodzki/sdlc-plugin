# Spec Delta

## ADDED Requirements

### Requirement: Execute state records the thread id
A new execute state file SHALL hold `t3ThreadId` when the server process runs in a T3 Code thread.

#### Scenario: New execute run in T3 Code
- **WHEN** `init` runs with `T3CODE_THREAD_ID` `abc-1`
- **THEN** the state file has `"t3ThreadId": "abc-1"`

### Requirement: Resolve-config thread facts
The `resolve-config` action SHALL return `t3ThreadId` and `shipDispatched`.

#### Scenario: Ship dispatched this run
- **WHEN** the ship state of the branch is not completed and its `execute` step is `in_progress`
- **THEN** `shipDispatched` is true

#### Scenario: Standalone run
- **WHEN** the branch has no ship state, or its `execute` step is `failed`, `completed` or `pending`
- **THEN** `shipDispatched` is false

#### Scenario: Ship status of the wrong type
- **WHEN** the ship `pipelineStatus` or the `execute` step status is not a string
- **THEN** `shipDispatched` is false
- **AND** `warnings` has one line that names the key and the type found

#### Scenario: Thread id set but not valid
- **WHEN** `T3CODE_THREAD_ID` is `abc/def`
- **THEN** the output has no `t3ThreadId`
- **AND** `warnings` has one line that names `T3CODE_THREAD_ID` and says that it is ignored

### Requirement: Read returns the run name
The `execute_state` `read` action SHALL return `runId`, the state file name without `.json`.

#### Scenario: Read after a compaction
- **WHEN** `execute_state` `read` runs for the branch
- **THEN** the output has `runId` and `t3ThreadId` when the state holds it
