# Spec Delta

## ADDED Requirements

### Requirement: Contract blocks as requirement statements
For a topic file, the skill SHALL read each `## Contracts` block whose label line lists a live `## Decisions` row as one requirement statement, and SHALL NOT read the `No contract:` line, or a block whose label line lists no live row, as a statement.

#### Scenario: Block of a live row
- **WHEN** `## Contracts` holds `**#2 Endpoint: POST /api/topics**` with its fence and row 2 is live
- **THEN** the Guardrails, Meaning and OpenSpec checks read that block as one requirement statement

#### Scenario: Block of a row that is not live
- **WHEN** a label line lists only `#4` and row 4 starts with `Moved:`
- **THEN** the block is not a requirement statement

#### Scenario: No-contract line
- **WHEN** `## Contracts` holds `No contract: #1.`
- **THEN** that line is not a requirement statement

#### Scenario: Empty Contracts section
- **WHEN** the topic file has a `## Contracts` heading with a blank body
- **THEN** the Structure findings hold the `empty-section` finding `Section ## Contracts is empty.`
