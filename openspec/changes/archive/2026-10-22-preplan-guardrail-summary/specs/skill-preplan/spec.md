# Spec Delta

## MODIFIED Requirements

### Requirement: Guardrail check per proposal
The skill SHALL check each live proposal against each plan guardrail at each Step 4, SHALL record a kept or dropped result as a note in the Reason cell of the `## Decisions` row, and SHALL call `plan_support` action `preplan_check` once at the end of Step 4.

- A live proposal is a `## Decisions` row whose Decision cell starts with neither `Dropped:` nor `Moved:`.
- The skill writes no other check text into the topic file: no basis, no pass list, no table.
- The skill never writes the `**Guardrail check:**` line itself.

| Conflict | Topic file record | Count |
|---|---|---|
| breaks an `error` guardrail after one rework | `Dropped:` at the start of the Decision cell, `Dropped by: <ids>.` at the end of the Reason cell | `droppedCount` |
| breaks only `warning` guardrails, user keeps it | `Kept with warning: <ids>.` at the end of the Reason cell | `keptCount` |
| guardrails loaded, no conflict | none; a kept note is deleted | `passCount` |
| guardrails unavailable | none; kept notes stay | `keptCount` with a kept note, else `passCount` |

#### Scenario: Error guardrail conflict
- **WHEN** a proposal still breaks an `error` guardrail `no-ci-bypass` after one rework
- **THEN** its Decision cell starts with `Dropped:`
- **AND** its Reason cell ends with `Dropped by: no-ci-bypass.`

#### Scenario: Warning guardrail conflict
- **WHEN** a proposal breaks a `warning` guardrail and its Reason cell has no kept note for that guardrail
- **THEN** the skill shows the reason and asks the user to keep, rework or cancel

#### Scenario: Kept proposal on a later check
- **WHEN** a proposal breaks only the `warning` guardrail `kiss` and its Reason cell ends with `Kept with warning: kiss.`
- **THEN** the skill asks no question about that proposal

#### Scenario: Check line written
- **WHEN** Step 4 ends with 6 pass, 1 kept and 0 dropped proposals
- **THEN** the skill calls `plan_support({ action: "preplan_check", topic: "<topic>", passCount: 6, keptCount: 1, droppedCount: 0 })`
- **AND** the skill reads the topic file again before its next edit
- **AND** the topic file has no `## Guardrail check` section

#### Scenario: Check call fails
- **WHEN** `preplan_check` returns an error
- **THEN** the skill prints the error's `## Do this` text and goes to the cancel step
- **AND** the status is `paused`

## ADDED Requirements

### Requirement: Open questions only when open
The skill SHALL keep a `## Open questions` section in the topic file only while at least one question has no answer.

- The skill adds the section after `## Decisions` with the first bullet.
- The skill deletes the whole section with the last bullet.

#### Scenario: First open question
- **WHEN** the topic file has no `## Open questions` section and an answer opens a new question
- **THEN** the file has a `## Open questions` section after `## Decisions` with one bullet

#### Scenario: Last question closed
- **WHEN** an answer closes the last bullet of `## Open questions`
- **THEN** the file has no `## Open questions` section

#### Scenario: Run again with no open question
- **WHEN** the user runs `/sdlc:preplan auth flow` again and the topic file has no `## Open questions` section
- **THEN** the skill prints `No open questions.`

### Requirement: Old topic file conversion
When the skill continues a topic file that has a `## Guardrail check` section, the skill SHALL move each kept and dropped result into a Reason cell note, and SHALL then delete the section.

- A note that the Reason cell already holds is not added again.
- The skill deletes the old section only after it writes every note.
- An empty `## Open questions` section is also deleted.

#### Scenario: Old kept row
- **WHEN** the old table has the row `| 2 | kiss | warning | kept — kiss |`
- **THEN** the Reason cell of `## Decisions` row 2 ends with `Kept with warning: kiss.`
- **AND** the file has no `## Guardrail check` section

#### Scenario: Conversion runs again
- **WHEN** a run stops after the notes are written but before the table is deleted, and the user runs the skill again
- **THEN** each Reason cell holds the note one time only
