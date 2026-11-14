# Spec Delta

## ADDED Requirements

### Requirement: Standalone instructions
Without `--no-instructions` and not in `--from-learnings` mode, the skill SHALL call `instructions_resolve` with key `harden` in Step 0, and SHALL run the list after Step 7 under the name `Run instructions`.

#### Scenario: From learnings
- **WHEN** `--from-learnings` is given
- **THEN** no instruction runs

#### Scenario: Stop in Step 5
- **WHEN** the run stops in Step 5
- **THEN** no instruction runs
