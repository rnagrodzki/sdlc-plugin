# Spec Delta

## ADDED Requirements

### Requirement: Run panel and run card in T3 Code
When `ship_prepare` returns `t3ThreadId`, the ship skill SHALL open the run panel and SHALL show the start card before the first pipeline step.

#### Scenario: Ship start in a T3 thread
- **WHEN** `/sdlc:ship` runs and `ship_prepare` returns `t3ThreadId`
- **THEN** the skill calls `dashboard` `ensure` with `open` true and `runId`
- **AND** it calls `preview_open` with `previewUrl`
- **AND** it calls `dashboard_card` with `runId`

#### Scenario: Outside T3 Code
- **WHEN** `ship_prepare` returns no `t3ThreadId`
- **THEN** the skill makes no panel call and no card call

#### Scenario: Resume and other modes
- **WHEN** the flag is `--resume`, `--dry-run`, `--gc` or `--init-config`
- **THEN** the skill makes no card call

### Requirement: End card
At each exit after the start card, the ship skill SHALL call `dashboard_card` with `runId` and `final` true.

#### Scenario: Ship ends
- **WHEN** the pipeline ends, completed or failed
- **THEN** the skill calls `dashboard_card` with `final` true

#### Scenario: Context compaction
- **WHEN** `runId` is not in context and the skill cannot tell that it started the run
- **THEN** the skill skips the end card

### Requirement: T3 failures never stop ship
A failed panel or card call MUST print one warning line and MUST NOT stop the run.

#### Scenario: Card fails
- **WHEN** `dashboard_card` returns an error
- **THEN** the skill prints `Warning: run card not shown — <first line of the error>.`
- **AND** the pipeline continues
