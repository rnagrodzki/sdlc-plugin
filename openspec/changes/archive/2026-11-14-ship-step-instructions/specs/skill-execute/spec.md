# Spec Delta

## ADDED Requirements

### Requirement: Standalone instructions
Without `--no-instructions`, the skill SHALL call `instructions_resolve` with key `execute` in Step 0, and SHALL run the list in Step 9 before `cleanup` through `execute_state` `instructions-next` and `instruction-record`.

#### Scenario: Failed run
- **WHEN** a task fails
- **THEN** no instruction runs and the records stay pending

#### Scenario: Resume
- **WHEN** `--resume` finds all tasks done and an open record
- **THEN** the skill runs the open records, then calls `cleanup`
