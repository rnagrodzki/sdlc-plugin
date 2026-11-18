# tool-dashboard-card Specification

## Purpose
Shows one ship or execute run as a read-only MCP App card in the chat of any client that supports MCP Apps, so the user sees the run state next to the chat. Other clients get the same run summary as text.

## Requirements

### Requirement: Card of one run
The `dashboard_card` tool SHALL return the run summary as Markdown text and as card data for the MCP App view `ui://sdlc/dashboard-card`.

#### Scenario: Ship run found
- **WHEN** `dashboard_card` gets `runId` `ship-feat-x-20261116T175538Z` and that run file exists
- **THEN** the result holds the run status, steps and issue counts
- **AND** the tool writes no file

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

### Requirement: Card run selector
The `dashboard_card` tool MUST accept only a ship or execute run name and MUST refuse a run of another T3 thread.

#### Scenario: Plan run name
- **WHEN** `runId` starts with `plan-`
- **THEN** the tool returns a DomainError that names `runId`

#### Scenario: Run name too long
- **WHEN** the part after `ship-` has 201 characters
- **THEN** the tool returns a DomainError

#### Scenario: Run of another thread
- **WHEN** the run has `t3ThreadId` `t-2` and the server thread is `t-1`
- **THEN** the tool returns DomainError `run <runId> belongs to another T3 thread`

#### Scenario: Thread id of the wrong type
- **WHEN** the server thread is `t-1` and the run stores `t3ThreadId` as a value that is not a string
- **THEN** the tool returns DataError `run <runId> has t3ThreadId of type <type>, expected string`

#### Scenario: Missing run
- **WHEN** no run file has the name `runId`
- **THEN** the tool returns DomainError `run <runId> not found`

### Requirement: Final card
The `dashboard_card` tool SHALL take `final` and SHALL show a run that is still `running` at a final call as stopped.

#### Scenario: End card of a stopped run
- **WHEN** `final` is true and the run status is `running`
- **THEN** the card shows `Stopped — resume with --resume`

### Requirement: Card view has no network access
The card view MUST use only inline script and style, and MUST NOT load a network address.

#### Scenario: Card HTML check
- **WHEN** the card HTML is built
- **THEN** it has no `src=`, no `fetch(` and no `XMLHttpRequest`
- **AND** it is 65536 bytes or less

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
        wave 1  2/2 tasks  completed
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
- **WHEN** the client shows no card and no dashboard server record exists (a broken link at the record path included), or the server does not answer its health probe
- **THEN** `text` has no `Dashboard:` line: its last line is the `ISSUES` line
- **AND** `next` tells the skill to call `dashboard` `ensure` with `runId`, then print its url with `#<runId>`
- **AND** the result has no `dashboardUrl` field

#### Scenario: Server record cannot be read
- **WHEN** the server record exists and cannot be read (a folder, bad JSON or no read permission)
- **THEN** the last line of `text` is `Dashboard: link unavailable`
- **AND** `next` orders no `dashboard` `ensure` call
- **AND** the card call returns no error

### Requirement: Snapshot card
The `dashboard_card` tool SHALL take `snapshot`. A snapshot card view MUST draw the run once and MUST NOT refresh. When `final` and `snapshot` are both true, `final` SHALL win.

#### Scenario: Middle card
- **WHEN** `snapshot` is true and `final` is false
- **THEN** the result has `snapshot` true
- **AND** the card view makes no refresh call

#### Scenario: Final and snapshot
- **WHEN** `final` is true and `snapshot` is true
- **THEN** the result has `final` true and no `snapshot` field
- **AND** `next` ends with `snapshot was ignored because final is true.`

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
