# Spec Delta

## ADDED Requirements

### Requirement: Template ship keys are commented examples
The shipped `plugins/sdlc/templates/local.toml` SHALL hold no live key under `[ship]`; each `[ship]` key SHALL be a commented example whose value equals the built-in ship default.

- The `[ship]` header stays live, so `local.toml` still carries `ship`.
- Every key keeps its tip comment.
- A new project uses the built-in ship defaults or the user file. An existing `local.toml` is not changed.

#### Scenario: No live ship key
- **WHEN** the shipped `local.toml` template is decoded as TOML
- **THEN** the `ship` table has zero keys

#### Scenario: Example values equal the built-in defaults
- **WHEN** each commented `[ship]` example except `quick` is read from the template
- **THEN** its value equals the built-in ship default for that key

#### Scenario: Changed example values
- **WHEN** the template is read
- **THEN** it holds `# auto = false`
- **AND** it holds `# steps = ["execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"]`

#### Scenario: Unchanged example values
- **WHEN** the template is read
- **THEN** it holds `# bump = "patch"`, `# draft = false`, `# rebase = true` and `# reviewThreshold = "info"`
- **AND** it holds `# executeWaveInterval = 60`, `# executeWaveTimeout = 1800`, `# verifyPipelineInterval = 60`, `# verifyPipelineMaxIterations = 3` and `# verifyPipelineTimeout = 1200`
- **AND** it holds `# awaitRemoteReviewers = ["copilot"]`, `# awaitRemoteReviewInterval = 60` and `# awaitRemoteReviewTimeout = 600`

#### Scenario: New project uses built-in ship defaults
- **WHEN** scaffold mode writes `.sdlc-v2/local.toml` in an empty directory
- **AND** no user file sets `[ship]`
- **THEN** the ship pipeline uses `auto = false`
- **AND** the ship steps are `execute`, `commit`, `review`, `archive-openspec`, `pr`, `learnings-commit`

### Requirement: Template quick key has no default
The shipped `local.toml` template SHALL keep `quick` as the commented example `# quick = ["execute", "commit", "review"]`, with a tip that says no built-in default exists.

#### Scenario: Quick tip
- **WHEN** the template is read
- **THEN** the line above `# quick = ["execute", "commit", "review"]` is `# No built-in default: when unset, --quick has no steps to run.`
