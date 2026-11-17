# Spec Delta

## ADDED Requirements

### Requirement: Plan state records the thread id
A new plan state file SHALL hold `t3ThreadId` when the server process runs in a T3 Code thread.

#### Scenario: New plan run in T3 Code
- **WHEN** `plan_prepare` starts a new run with `T3CODE_THREAD_ID` `abc-1`
- **THEN** the plan state file has `"t3ThreadId": "abc-1"`

#### Scenario: Outside T3 Code
- **WHEN** `T3CODE_THREAD_ID` is not set
- **THEN** the plan state file has no `t3ThreadId` key
