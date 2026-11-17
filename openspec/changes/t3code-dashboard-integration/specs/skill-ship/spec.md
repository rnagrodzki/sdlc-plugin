# Spec Delta

## ADDED Requirements

### Requirement: Run card in every client
The ship skill SHALL show the start card before the first pipeline step in every client. The `dashboard_card` tool decides if the client can show the card.

#### Scenario: Ship start
- **WHEN** `/sdlc:ship` runs past the confirmation
- **THEN** the skill calls `dashboard_card` with `runId`

#### Scenario: Client shows no card
- **WHEN** the `next` of a card result starts with `This client shows no card`
- **THEN** the skill makes no more card calls in this run, the end card included

### Requirement: Run panel in T3 Code only
When `ship_prepare` returns `t3ThreadId`, the ship skill SHALL open the run panel before the start card. The skill MUST NOT open the run panel or print a dashboard URL when `t3ThreadId` is absent.

#### Scenario: Ship start in a T3 thread
- **WHEN** `/sdlc:ship` runs and `ship_prepare` returns `t3ThreadId`
- **THEN** the skill calls `dashboard` `ensure` with `open` true and `runId`
- **AND** it calls `preview_open` with `previewUrl`
- **AND** it calls `dashboard_card` with `runId`

#### Scenario: Outside T3 Code
- **WHEN** `ship_prepare` returns no `t3ThreadId`
- **THEN** the skill makes no panel call
- **AND** it calls `dashboard_card` with `runId`

#### Scenario: Resume and other modes
- **WHEN** the flag is `--resume`, `--dry-run`, `--gc` or `--init-config`
- **THEN** the skill makes no card call

### Requirement: End card
At each exit after the start card, the ship skill SHALL call `dashboard_card` with `runId` and `final` true, unless a card result of the run said that the client shows no card.

#### Scenario: Ship ends
- **WHEN** the pipeline ends, completed or failed
- **THEN** the skill calls `dashboard_card` with `final` true

#### Scenario: Context compaction
- **WHEN** `runId` is not in context and the skill cannot tell that it started the run
- **THEN** the skill skips the end card

### Requirement: Panel and card failures never stop ship
A failed panel or card call MUST print one warning line and MUST NOT stop the run.

#### Scenario: Card fails
- **WHEN** `dashboard_card` returns an error
- **THEN** the skill prints `Warning: run card not shown — <first line of the error>.`
- **AND** the pipeline continues
