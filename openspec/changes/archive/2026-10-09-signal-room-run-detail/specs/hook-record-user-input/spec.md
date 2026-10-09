# Spec Delta

## ADDED Requirements

### Requirement: Answer closes the question wait
The `record-user-answer` hook SHALL delete the question wait record of its tool use id before any other check. With no tool use id, it SHALL delete every question record of the session.

#### Scenario: Answer arrives
- **WHEN** the user answers the question with tool use id `toolu_9`
- **THEN** the file `s1-toolu_9.json` does not exist

#### Scenario: No tool use id
- **WHEN** the answer event has no `tool_use_id`
- **THEN** no question record of the session exists

### Requirement: Prompt closes every wait
The `record-user-input` hook SHALL delete every wait record of the session when it keeps the prompt. An injected turn that the hook drops SHALL delete nothing.

#### Scenario: Typed prompt
- **WHEN** the user types a prompt in session `s1`
- **THEN** no wait record of `s1` exists

#### Scenario: Injected turn
- **WHEN** the prompt is a `<task-notification>` turn
- **THEN** the wait records of the session stay
