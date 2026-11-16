# Spec Delta

## ADDED Requirements

### Requirement: Check against OpenSpec at the handoff
When the project has `openspec/config.yaml`, the preplan handoff question SHALL offer `Check against OpenSpec`. The option SHALL run `/sdlc:verify-requirements --topic <topic>`, then ask the handoff question again.

#### Scenario: OpenSpec project
- **WHEN** `openspec/config.yaml` exists and the topic is ready for plan
- **THEN** the handoff question has 4 options, one of them `Check against OpenSpec`

#### Scenario: No OpenSpec
- **WHEN** `openspec/config.yaml` does not exist
- **THEN** the handoff question has 3 options

#### Scenario: Check stops with an error
- **WHEN** the check stops with an error
- **THEN** the skill prints the error and asks the handoff question again
