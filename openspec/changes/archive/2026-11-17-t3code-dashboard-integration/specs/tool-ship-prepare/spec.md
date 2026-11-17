# Spec Delta

## ADDED Requirements

### Requirement: Thread id output
The `ship_prepare` tool SHALL return `t3ThreadId` when the server process runs in a T3 Code thread.

#### Scenario: In a T3 thread
- **WHEN** `T3CODE_THREAD_ID` is `t-1`
- **THEN** the output has `t3ThreadId` `t-1`

#### Scenario: Outside T3 Code
- **WHEN** `T3CODE_THREAD_ID` is not set or not valid
- **THEN** the output has no `t3ThreadId`

#### Scenario: Thread id set but not valid
- **WHEN** `T3CODE_THREAD_ID` is `abc/def`
- **THEN** the output has no `t3ThreadId`
- **AND** `warnings` has one line that names `T3CODE_THREAD_ID` and says that it is ignored
