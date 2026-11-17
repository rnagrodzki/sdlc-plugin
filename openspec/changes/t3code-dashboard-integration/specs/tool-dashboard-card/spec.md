# Spec Delta

## Purpose

Shows one ship or execute run as a read-only MCP App card in a T3 Code chat, so the user sees the run state next to the thread.

## ADDED Requirements

### Requirement: Card of one run
The `dashboard_card` tool SHALL return the run summary as Markdown text and as card data for the MCP App view `ui://sdlc/dashboard-card`.

#### Scenario: Ship run found
- **WHEN** `dashboard_card` gets `runId` `ship-feat-x-20261116T175538Z` and that run file exists
- **THEN** the result holds the run status, steps and issue counts
- **AND** the tool writes no file

#### Scenario: Outside T3 Code
- **WHEN** `T3CODE_THREAD_ID` is not set
- **THEN** the result `next` is `No T3 Code thread: no card renders here. The text is the run summary. Continue.`

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
