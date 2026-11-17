# Spec Delta

## MODIFIED Requirements

### Requirement: Client support for the card
The `dashboard_card` tool SHALL treat a client as one that shows the card when the client announces the MCP Apps extension `io.modelcontextprotocol/ui` in `initialize` `capabilities.extensions`, or when `T3CODE_THREAD_ID` holds a valid thread id. The tool MUST return the run data and no error for every other client.

#### Scenario: Client announces MCP Apps
- **WHEN** the client announces `io.modelcontextprotocol/ui` and the run status is `running`
- **THEN** the result `next` is `Card shows run <runId> (running). Continue the current step.`

#### Scenario: T3 thread fallback
- **WHEN** the client announces no extension and `T3CODE_THREAD_ID` is a valid thread id
- **THEN** the result `next` is `Card shows run <runId> (<status>). Continue the current step.`

#### Scenario: Client shows no card
- **WHEN** the client does not announce `io.modelcontextprotocol/ui` and `T3CODE_THREAD_ID` is not set
- **THEN** the result `next` starts with `This client shows no card. Print the text field in the chat unchanged, in a code block.`
- **AND** the result `next` ends with `Call dashboard_card again at the next card point of this run.`
- **AND** the result holds the run status, steps and issue counts, with no error

## ADDED Requirements

### Requirement: Text card for a client with no card
For a client that shows no card, the `dashboard_card` tool SHALL return the run card as a fixed-width text block in the `text` field. A client that shows the card MUST get no `text` field.

```text
SHIP  [running]
ship-feat-x-20261116T175538Z
Branch:    feat/x
Started:   2026-11-16T17:55:38Z
Completed: not recorded

STEPS                                   1 of 4 done
[#>..]
  [x] execute            completed
        wave 1   2/2 tasks   completed
  [>] commit             in_progress
  [ ] review             pending
  [ ] pr                 pending

ISSUES   high 1
Dashboard: http://127.0.0.1:55551#ship-feat-x-20261116T175538Z
```

#### Scenario: Ship run in a client with no card
- **WHEN** the client shows no card and the ship run has steps execute, commit, review and pr
- **THEN** `text` has one row for each step with the markers `[x]`, `[>]`, `[ ]`, `[-]` or `[!]`
- **AND** `text` has the line `STEPS` with `<done> of <total> done`

#### Scenario: Run with no steps
- **WHEN** the run has 0 steps
- **THEN** `text` has the line `No steps yet.` in place of the step rows

#### Scenario: Stopped run at the end card
- **WHEN** `final` is true and the run status is `running`
- **THEN** the first line of `text` ends with `[stopped]`

#### Scenario: Card client
- **WHEN** the client announces `io.modelcontextprotocol/ui`
- **THEN** the result has no `text` field and no `dashboardUrl` field

### Requirement: Dashboard link in the text card
The `dashboard_card` tool SHALL read the dashboard server status and MUST NOT start a server. When the server runs, the tool SHALL return `dashboardUrl` as `<url>#<runId>`. A server status that cannot be read MUST NOT fail the card call.

#### Scenario: Server runs
- **WHEN** the client shows no card and the dashboard server answers its health probe
- **THEN** `dashboardUrl` is `<url>#<runId>`
- **AND** the last line of `text` is `Dashboard: <url>#<runId>`

#### Scenario: No server
- **WHEN** the client shows no card and no dashboard server record exists
- **THEN** the last line of `text` is `Dashboard: not running`
- **AND** `next` tells the skill to call `dashboard` `ensure` with `runId` if no ensure ran in this run

#### Scenario: Server record cannot be read
- **WHEN** the server record is a folder, a broken link or bad JSON
- **THEN** the last line of `text` is `Dashboard: not running`
- **AND** the card call returns no error

### Requirement: Snapshot card
The `dashboard_card` tool SHALL take `snapshot`. A snapshot card view MUST draw the run once and MUST NOT refresh. When `final` and `snapshot` are both true, `final` SHALL win.

#### Scenario: Middle card
- **WHEN** `snapshot` is true and `final` is false
- **THEN** the result has `snapshot` true
- **AND** the card view makes no refresh call

#### Scenario: Final and snapshot
- **WHEN** `final` is true and `snapshot` is true
- **THEN** the result has `final` true and `snapshot` false

### Requirement: Wave task counters
The `dashboard_card` tool SHALL give each `wave <n>` step of an execute run the count of tasks with status `completed` and the count of all tasks of the wave.

#### Scenario: Wave with a failed task
- **WHEN** wave 2 has 5 tasks, 3 `completed` and 1 `failed`
- **THEN** the `wave 2` step has `tasksDone` 3 and `tasksTotal` 5

#### Scenario: Planned wave not started
- **WHEN** wave 3 has 4 planned tasks and did not start
- **THEN** the `wave 3` step has `tasksDone` 0 and `tasksTotal` 4

#### Scenario: Queued step
- **WHEN** the execute run has a `queued` step
- **THEN** that step has no task counters

### Requirement: Ship card shows execute waves
The `dashboard_card` tool SHALL show the waves of the execute run that the dashboard page joins to a ship run, under the ship `execute` step. A ship run with no joined execute run MUST give the same card data as before this change.

#### Scenario: Joined execute run
- **WHEN** an execute run of the same branch is inside the time window of the ship run
- **THEN** the ship `execute` step has `waves` with one row for each wave, with `tasksDone` and `tasksTotal`

#### Scenario: Two execute runs in the window
- **WHEN** two execute runs of the same branch are inside the time window of the ship run
- **THEN** the card shows the waves of the newer execute run

#### Scenario: No joined execute run
- **WHEN** no execute run of the same branch is inside the time window of the ship run
- **THEN** the ship `execute` step has no `waves`
