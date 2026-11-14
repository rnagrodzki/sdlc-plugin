# Spec Delta

## MODIFIED Requirements

### Requirement: No commits and no outside writes
The skill SHALL NOT run `git commit`, `git push`, `git tag`, or any other state-changing git or `gh` command, and SHALL NOT modify files outside the project root. The only exception is an instruction from the `[instructions]` config in a standalone run.

- A fix edits source files only; the caller (ship, through the `commit` skill) commits and pushes.

#### Scenario: Fix is left uncommitted
- **WHEN** the skill applies a fix and emits `fix-applied`
- **THEN** no commit, push, or tag was created by the skill
- **AND** the edited files remain as uncommitted changes

## ADDED Requirements

### Requirement: Standalone instructions
Without `--no-instructions`, the skill SHALL call `instructions_resolve` with key `verify-pipeline` after Step 1, and SHALL run the list in interactive mode after a `fix-applied` or `proposal` verdict line.

#### Scenario: Summary after the JSON line
- **WHEN** a standalone run ends with `fix-applied`
- **THEN** the JSON verdict line comes first
- **AND** the `Instructions (verify-pipeline): …` line follows it

#### Scenario: Ship dispatch
- **WHEN** ship dispatches the skill with `--no-instructions`
- **THEN** the skill prints only the JSON verdict line

#### Scenario: Abort verdict
- **WHEN** the verdict is `abort`
- **THEN** no instruction runs
