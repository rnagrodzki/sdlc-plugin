# Spec Delta

## ADDED Requirements

### Requirement: Standalone instructions
Without `--no-instructions`, the skill SHALL call `instructions_resolve` with key `received-review` before Step 0, and SHALL run the list after Step 12.5 with its own `--auto` mode.

#### Scenario: Cancel
- **WHEN** the user cancels at Step 10
- **THEN** no instruction runs

#### Scenario: Ship dispatch
- **WHEN** ship dispatches the skill with `--no-instructions`
- **THEN** the skill does not call `instructions_resolve`
