# Spec Delta

## ADDED Requirements

### Requirement: Run card for a standalone run
When `resolve-config` returns `shipDispatched` false and the run uses the wave route, the execute skill SHALL show the start card and the end card in every client. The `dashboard_card` tool decides if the client can show the card.

#### Scenario: Standalone execute
- **WHEN** `/sdlc:execute` runs with `shipDispatched` false and without `--resume`
- **THEN** the skill calls `dashboard_card` with `runId` after `init`
- **AND** it calls `dashboard_card` with `runId` and `final` true at the end of the run

#### Scenario: Client shows no card
- **WHEN** the `next` of a card result starts with `This client shows no card`
- **THEN** the skill makes no more card calls in this run, the end card included

#### Scenario: Direct route
- **WHEN** the run uses the Step 2b direct route
- **THEN** the skill makes no card call

### Requirement: Run panel in T3 Code only
When `resolve-config` returns `t3ThreadId` and `shipDispatched` false, the execute skill SHALL open the run panel before the start card. The skill MUST NOT open the run panel or print a dashboard URL when `t3ThreadId` is absent.

#### Scenario: Standalone execute in a T3 thread
- **WHEN** `/sdlc:execute` runs, `t3ThreadId` is set and `shipDispatched` is false
- **THEN** the skill calls `dashboard` `ensure` with `runId`, then `preview_open`, then `dashboard_card`

#### Scenario: Standalone execute outside T3 Code
- **WHEN** `/sdlc:execute` runs, `t3ThreadId` is absent and `shipDispatched` is false
- **THEN** the skill makes no panel call
- **AND** it calls `dashboard_card` with `runId`

#### Scenario: Execute dispatched by ship
- **WHEN** `shipDispatched` is true
- **THEN** the skill makes no panel call and no card call

#### Scenario: Resume
- **WHEN** the flag is `--resume`
- **THEN** the skill makes no panel call and no card call

### Requirement: Panel and card failures never stop execute
A failed panel or card call MUST print one warning line and MUST NOT stop the run.

#### Scenario: Panel fails
- **WHEN** `preview_open` fails
- **THEN** the skill prints `Warning: run panel not opened — ` and the first line of the error, at most 200 characters, in an inline code span
- **AND** the run continues
