# Spec Delta

## MODIFIED Requirements

### Requirement: Several preplan topic files
With two or more paths, the skill SHALL read each preplan topic file in argument order, SHALL number the R-items across all files, and SHALL ask one Structured discovery question for each conflict between two files.

#### Scenario: Two topic files
- **WHEN** the user runs `/sdlc:plan ".sdlc-v2/preplan/2026-10-29-a.md" ".sdlc-v2/preplan/2026-10-29-b.md"`
- **THEN** the requirements of file a come before those of file b, numbered R1 to Rn without a restart

#### Scenario: Per-topic entries
- **WHEN** two topic files are read
- **THEN** `## Key Decisions` has one Non-goals bullet and one Smallest cut bullet for each topic, with the topic name first

#### Scenario: Per-topic contract bullets
- **WHEN** two topic files each hold a live block with the label line `**#2 Endpoint: POST /x**`
- **THEN** each `Contract Endpoint: POST /x` bullet in `## Key Decisions` starts with the name of its topic

#### Scenario: Conflict under auto mode
- **WHEN** `--auto` is set and two files have decisions that contradict each other
- **THEN** the skill takes the decision of the earlier file
- **AND** it adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Source header
- **WHEN** two topic files are read and the OpenSpec gate check sets no source
- **THEN** `**Source:**` lists both paths separated by `, `

#### Scenario: Path that is not a topic file
- **WHEN** the second path is `README.md`
- **THEN** the skill stops and prints `README.md is not a preplan topic file. Run /sdlc:plan with topic files only, or with one requirements file.`

## ADDED Requirements

### Requirement: Topic contracts bind plan tasks
When a preplan topic file has `## Contracts` blocks, the plan skill SHALL write one `## Key Decisions` bullet `Contract <Kind>: <name>` and one requirement item for each block whose label lists a live `## Decisions` row, and the task that builds that surface SHALL copy the block into the `shape` of its `**Contract:**`.

- The `No contract:` line adds nothing.
- A task that changes the shape adds one `## Deviations & assumptions` row that names the contract.

#### Scenario: Live block
- **WHEN** the topic file holds the block `**#2 Endpoint: POST /api/topics**` and row 2 is live
- **THEN** `## Key Decisions` holds the bullet `Contract Endpoint: POST /api/topics` with the block
- **AND** one requirement item records the contract
- **AND** the task that builds the endpoint copies the block into the `shape` of its `**Contract:**`

#### Scenario: Block of a row that is not live
- **WHEN** a label line lists only `#4` and row 4 starts with `Dropped:`
- **THEN** the plan has no Key Decisions bullet and no requirement item for that block

#### Scenario: No-contract line
- **WHEN** `## Contracts` holds `No contract: #1, #5.`
- **THEN** that line adds no Key Decisions bullet and no requirement item

#### Scenario: Missing contracts in an interactive run
- **WHEN** `## Contracts` is empty or absent, or a live row has no contract answer, and `--auto` is not set
- **THEN** the skill asks one Structured discovery question: `Which endpoints, data shapes, config keys or messages does this change add or change, and what is the exact shape of each?`

#### Scenario: Missing contracts under auto mode
- **WHEN** `## Contracts` is empty and `--auto` is set
- **THEN** the skill asks no question
- **AND** `## Key Decisions` holds the bullet `Contracts: not stated for <the Decision text of each live row with no answer>; each task decides its Contract shape`
- **AND** `## Deviations & assumptions` has one row with `asked=no`

#### Scenario: Template with no Key Decisions
- **WHEN** the active plan template does not list `## Key Decisions`
- **THEN** the skill records each live contract as a requirement item only
