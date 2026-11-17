# Spec Delta

## ADDED Requirements

### Requirement: Run panel and run card for a standalone run
When `resolve-config` returns `t3ThreadId` and `shipDispatched` false, the execute skill SHALL open the run panel and SHALL show the start card and the end card.

#### Scenario: Standalone execute in a T3 thread
- **WHEN** `/sdlc:execute` runs, `t3ThreadId` is set and `shipDispatched` is false
- **THEN** the skill calls `dashboard` `ensure` with `runId`, then `preview_open`, then `dashboard_card`

#### Scenario: Execute dispatched by ship
- **WHEN** `shipDispatched` is true
- **THEN** the skill makes no panel call and no card call

#### Scenario: Resume
- **WHEN** the flag is `--resume`
- **THEN** the skill makes no card call

### Requirement: T3 failures never stop execute
A failed panel or card call MUST print one warning line and MUST NOT stop the run.

#### Scenario: Panel fails
- **WHEN** `preview_open` fails
- **THEN** the skill prints `Warning: run panel not opened — <first line of the error>.`
- **AND** the run continues
