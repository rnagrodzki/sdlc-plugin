# Spec Delta

## ADDED Requirements

### Requirement: Standalone instructions
Without `--no-instructions`, the skill SHALL call `instructions_resolve` with key `review` before Step 0, and SHALL run the list in interactive mode after Step 9.

#### Scenario: Dry run
- **WHEN** `--dry-run` is given
- **THEN** no instruction runs

#### Scenario: Success
- **WHEN** the review posts its verdict
- **THEN** the skill runs the list and prints `Instructions (review): …`
