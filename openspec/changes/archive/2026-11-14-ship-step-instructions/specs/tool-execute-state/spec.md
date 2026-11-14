# Spec Delta

## ADDED Requirements

### Requirement: Instruction actions for standalone execute
The tool SHALL add `instructions-next` and `instruction-record` for the key `execute`, with the inputs `instructionNumber`, `instructionAll`, `instructionStatus` and `instructionOutcome`. The first `instructions-next` call SHALL seed the execute list from config.

#### Scenario: First call
- **WHEN** the state has no `instructions` key
- **THEN** the tool seeds the execute list and returns `execute #1`

#### Scenario: Empty list
- **WHEN** the execute list is empty
- **THEN** `next` says `No instructions for execute. Call cleanup.`

#### Scenario: Input with another action
- **WHEN** `cleanup` gets `instructionStatus`
- **THEN** the tool returns DomainError `instructionStatus is valid only with instruction-record`
