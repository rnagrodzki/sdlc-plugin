# Spec Delta

## MODIFIED Requirements

### Requirement: High-risk wave gate
The skill SHALL ask for approval before a high-risk wave unless effective auto or `highRiskAutoApprove` is true.

| Answer | Skill action |
|---|---|
| `yes` | Proceed with the wave |
| `skip` | Do not dispatch the wave; `task-fail` each of its tasks and `wave-fail` the wave, both with error `skipped by user at the high-risk gate`; continue with the next wave |
| `cancel` | Stop before `wave-start`; do not call `cleanup`; print the "Execution state preserved at ..." line |

- After a `skip`, a later task that depends on a skipped task is recorded with `task-fail` and `skippedDependency: true` instead of being dispatched.
- The final report lists every skipped task as not implemented.
- A saved setting is a deciding source with the label `local` (a value from `.sdlc-v2/local.toml` or `~/.sdlc/local.toml`) or `config`. Either label prints the saved-setting warning.

#### Scenario: High-risk wave, interactive
- **WHEN** a wave is high-risk
- **AND** effective auto and `highRiskAutoApprove` are both false
- **THEN** the skill asks with AskUserQuestion (`yes`/`skip`/`cancel`) before `wave-start`

#### Scenario: User skips a high-risk wave
- **WHEN** the user answers `skip`
- **THEN** no task of that wave is dispatched
- **AND** each of its tasks is recorded with `task-fail` and the wave with `wave-fail`
- **AND** the run continues with the next wave

#### Scenario: User cancels at the high-risk gate
- **WHEN** the user answers `cancel`
- **THEN** the skill stops before `wave-start` without calling `cleanup`
- **AND** the state file stays resumable

#### Scenario: Auto-approved from a saved config file
- **WHEN** a high-risk wave proceeds without a prompt
- **AND** `sources.highRiskAutoApprove` or `sources.auto` is `"config"`
- **THEN** before dispatch the skill prints a line starting `WARNING: high-risk wave <N> auto-approved from .sdlc-v2/local.toml`

#### Scenario: Auto-approved from a local file
- **WHEN** a high-risk wave proceeds without a prompt
- **AND** `sources.highRiskAutoApprove` or `sources.auto` is `"local"`
- **THEN** before dispatch the skill prints a line starting `WARNING: high-risk wave <N> auto-approved from .sdlc-v2/local.toml`

#### Scenario: Auto-approved from a per-run source
- **WHEN** a high-risk wave proceeds without a prompt
- **AND** the deciding source is `"cli"` or `"pipeline"`
- **THEN** the skill prints no warning
