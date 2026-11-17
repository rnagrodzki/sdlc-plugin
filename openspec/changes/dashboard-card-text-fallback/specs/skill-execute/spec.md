# Spec Delta

## MODIFIED Requirements

### Requirement: Run card for a standalone run
When `resolve-config` returns `shipDispatched` false and the run uses the wave route, the execute skill SHALL show the start card and the end card in every client. The `dashboard_card` tool decides if the client can show the card. In a client with no card, the skill SHALL print the `text` field at each card point and SHALL keep the card points.

#### Scenario: Standalone execute
- **WHEN** `/sdlc:execute` runs with `shipDispatched` false and without `--resume`
- **THEN** the skill calls `dashboard_card` with `runId` after `init`
- **AND** it calls `dashboard_card` with `runId` and `final` true at the end of the run

#### Scenario: Client shows no card
- **WHEN** the `next` of a card result starts with `This client shows no card`
- **THEN** the skill prints the `text` field unchanged, in a code block
- **AND** the skill calls `dashboard_card` again at the next card point of this run, the end card included

#### Scenario: Direct route
- **WHEN** the run uses the Step 2b direct route
- **THEN** the skill makes no card call
- **AND** it prints `Run card: not shown on the direct route.`

### Requirement: Run panel in T3 Code only
When `resolve-config` returns `t3ThreadId` and `shipDispatched` false, the execute skill SHALL open the run panel before the start card. The skill MUST NOT open the run panel when `t3ThreadId` is absent. Outside T3 Code, the skill prints a dashboard link only by the rule of the text card.

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
- **AND** it prints `Run card: not shown on a resumed run.`

### Requirement: Panel and card failures never stop execute
A failed panel or card call MUST NOT stop the run. The skill SHALL print one warning line at the first card error of the run and nothing at a later card error.

#### Scenario: Panel fails
- **WHEN** `preview_open` fails
- **THEN** the skill prints `Warning: run panel not opened — ` and the first line of the error, at most 200 characters, in an inline code span
- **AND** the run continues

#### Scenario: Card fails again
- **WHEN** `dashboard_card` returns an error and an earlier card call of the run failed
- **THEN** the skill prints no warning
- **AND** the run continues

## ADDED Requirements

### Requirement: Wave card after each wave
In a standalone run on the wave route, the execute skill SHALL call `dashboard_card` with `runId` and `snapshot` true at stage 6 of each wave that another wave follows. After the last wave, the end card follows and no wave card is shown.

#### Scenario: Wave 1 of 3 ends
- **WHEN** wave 1 of 3 reaches stage 6
- **THEN** the skill calls `dashboard_card` with `runId` and `snapshot` true

#### Scenario: Last wave ends
- **WHEN** wave 3 of 3 reaches stage 6
- **THEN** the skill makes no wave card call
- **AND** it calls `dashboard_card` with `final` true

#### Scenario: Ensure for the dashboard link
- **WHEN** a card `next` names `dashboard({action:"ensure"` and no ensure ran in this run
- **THEN** the skill calls `dashboard` `ensure` with `runId` and no `open`
- **AND** it prints `Dashboard: <url>#<runId>`
