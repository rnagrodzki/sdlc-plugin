# Spec Delta

## MODIFIED Requirements

### Requirement: Handoff to plan
When the topic is ready, the skill SHALL set the status `ready for plan` with `preplan_write`, SHALL offer Start plan now, Start a new idea and Stop, and SHALL keep the status `ready for plan` on each of these paths.

#### Scenario: User starts the plan
- **WHEN** the user picks Start plan now and `readyTopicFiles` holds only this file
- **THEN** the skill runs `/sdlc:plan` with the topic file path in double quotes in the same session

#### Scenario: Several ready topic files
- **WHEN** the user picks Start plan now and `readyTopicFiles` holds 3 paths
- **THEN** the skill asks which paths to include, with this file first
- **AND** the skill runs `/sdlc:plan` with each picked path in double quotes, in option order

#### Scenario: Start a new idea
- **WHEN** the user picks Start a new idea and answers `export flow`
- **THEN** the skill runs `/sdlc:preplan export flow` in the same session
- **AND** this topic file keeps the status `ready for plan`

#### Scenario: Plan skill fails
- **WHEN** the plan skill stops with an error
- **THEN** the skill prints the error and tells the user to run `/sdlc:plan` with the picked paths

## ADDED Requirements

### Requirement: Topic file writes through the tool
The skill SHALL change the topic file only through the `plan_support` actions `preplan_context`, `preplan_check` and `preplan_write`, and SHALL NOT use a file edit or a file write on it.

#### Scenario: Plan mode is active
- **WHEN** the user runs `/sdlc:preplan auth flow` while plan mode is active and answers a question
- **THEN** the topic file holds the answer before the next question

#### Scenario: Refused body
- **WHEN** `preplan_write` refuses the body of one answer for the first time
- **THEN** the skill fixes the body and sends the call again

#### Scenario: Third refusal
- **WHEN** `preplan_write` refuses the body of one answer three times
- **THEN** the skill prints the error and goes to the cancel step
- **AND** the status is `paused`

### Requirement: No plan warning for a preplan session
The stop hook SHALL NOT print `Plan presented but plan was not invoked` when the transcript, in its first 64 MiB, holds a `/sdlc:preplan` command record or a Skill call for `sdlc:preplan`.

#### Scenario: Preplan session in plan mode
- **WHEN** the transcript holds `<command-name>/sdlc:preplan</command-name>` and later `Plan mode is active`, and no plan state file exists
- **THEN** the stop hook prints no warning

#### Scenario: No preplan record
- **WHEN** the transcript holds `Plan mode is active` and no preplan record, and no plan state file exists
- **THEN** the stop hook prints the warning

#### Scenario: Transcript cannot be read
- **WHEN** the transcript path is a folder and no plan state file exists
- **THEN** the stop hook prints no warning, because an unreadable transcript gives no evidence of plan mode
