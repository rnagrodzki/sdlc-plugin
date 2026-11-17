# Spec Delta

## MODIFIED Requirements

### Requirement: Run card in every client
The ship skill SHALL show the start card before the first pipeline step in every client. The `dashboard_card` tool decides if the client can show the card. In a client with no card, the skill SHALL print the `text` field at each card point and SHALL keep the card points.

#### Scenario: Ship start
- **WHEN** `/sdlc:ship` runs past the confirmation
- **THEN** the skill calls `dashboard_card` with `runId`

#### Scenario: Client shows no card
- **WHEN** the `next` of a card result starts with `This client shows no card`
- **THEN** the skill prints the `text` field unchanged, in a code block
- **AND** the skill calls `dashboard_card` again at the next card point of this run, the end card included

### Requirement: Run panel in T3 Code only
When `ship_prepare` returns `t3ThreadId`, the ship skill SHALL open the run panel before the start card. The skill MUST NOT open the run panel when `t3ThreadId` is absent. Outside T3 Code, the skill prints a dashboard link only by the rule of the text card.

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
At each exit after the start card, the ship skill SHALL call `dashboard_card` with `runId` and `final` true, in every client.

#### Scenario: Ship ends
- **WHEN** the pipeline ends, completed or failed
- **THEN** the skill calls `dashboard_card` with `final` true

#### Scenario: Context compaction
- **WHEN** `runId` is not in context and the skill cannot tell that it started the run
- **THEN** the skill skips the end card

### Requirement: Panel and card failures never stop ship
A failed panel or card call MUST NOT stop the run. The skill SHALL print one warning line at the first card error of the run and nothing at a later card error.

#### Scenario: Card fails
- **WHEN** `dashboard_card` returns an error for the first time in the run
- **THEN** the skill prints `Warning: run card not shown — ` and the first line of the error, at most 200 characters, in an inline code span
- **AND** the pipeline continues

#### Scenario: Card fails again
- **WHEN** `dashboard_card` returns an error and an earlier card call of the run failed
- **THEN** the skill prints no warning
- **AND** the pipeline continues

## ADDED Requirements

### Requirement: Step card at each ship step
At item 7b of each step after the first step of the invocation, the ship skill SHALL call `dashboard_card` with `runId`. The skill SHALL pass `snapshot` true for each step except `execute`. The `execute` step card SHALL be live.

#### Scenario: Commit step starts
- **WHEN** the step `commit` starts and it is not the first step of the invocation
- **THEN** the skill calls `dashboard_card` with `runId` and `snapshot` true

#### Scenario: Execute step starts
- **WHEN** the step `execute` starts and it is not the first step of the invocation
- **THEN** the skill calls `dashboard_card` with `runId` and no `snapshot`

#### Scenario: First step of the invocation
- **WHEN** a step is the first step of the invocation
- **THEN** the skill makes no step card call, because the live start card covers it

### Requirement: Dashboard link fallback in ship
When a card `next` tells the skill to call `dashboard` `ensure`, the ship skill SHALL call it at most once in the run, with `runId` and no `open`. The skill MUST NOT open a browser.

#### Scenario: Ensure succeeds
- **WHEN** the card `next` names `dashboard({action:"ensure"` and no ensure ran in this run
- **THEN** the skill calls `dashboard` `ensure` with `runId`
- **AND** it prints `Dashboard: <url>#<runId>`

#### Scenario: Ensure fails
- **WHEN** `dashboard` `ensure` returns an error
- **THEN** the skill prints `Warning: dashboard not started — ` and the error text
- **AND** the skill makes no more ensure calls in this run

#### Scenario: Resumed run
- **WHEN** the flag is `--resume`
- **THEN** the skill prints `Run card: not shown on a resumed run.`
