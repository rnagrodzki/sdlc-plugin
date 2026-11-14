# Spec Delta

## ADDED Requirements

### Requirement: Push flag
With `--push`, the skill SHALL pass `push: true` to `commit_apply` and SHALL print each push warning.

#### Scenario: Default branch
- **WHEN** `--push` is given on the default branch
- **THEN** the commit lands and the skill prints `push skipped: <branch> is the default branch`

### Requirement: Standalone instructions
Without `--no-instructions`, the skill SHALL call `instructions_resolve` with key `commit` before Step 0, and SHALL run the list after Step 6 and print `Instructions (commit): …`.

#### Scenario: Config error
- **WHEN** `instructions_resolve` returns a DataError
- **THEN** the skill prints the error and stops before Step 0

#### Scenario: Interrupt
- **WHEN** the list stops before its end
- **THEN** the skill prints `Run the remaining instructions by hand. A new run of the skill runs the whole list again unless you pass --no-instructions.`
