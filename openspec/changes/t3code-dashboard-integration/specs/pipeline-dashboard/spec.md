# Spec Delta

## ADDED Requirements

### Requirement: Thread filter
The dashboard page SHALL show only the pipelines of one T3 thread when the address has `?thread=<id>`.

#### Scenario: Filter by thread
- **WHEN** the address is `#pipelines?thread=t-1`
- **THEN** the Pipelines tab shows only pipelines with `t3ThreadId` `t-1`

#### Scenario: Thread chip
- **WHEN** a pipeline has `t3ThreadId`
- **THEN** its block shows a chip with the first 8 characters and the full id as the title

### Requirement: Pending run hash
A run hash whose block is not drawn yet SHALL stay pending until the run shows in a snapshot.

#### Scenario: Run panel opens before the run is listed
- **WHEN** the address is `#<runId>?thread=t-1` and the snapshot has no block for `runId`
- **THEN** the page keeps the hash
- **AND** it scrolls to the block once, after the snapshot that holds the run

#### Scenario: User leaves the pending hash
- **WHEN** the user picks a tab while a hash is pending
- **THEN** the page clears the pending hash

### Requirement: Page fits a narrow panel
The dashboard page SHALL show its header and History with no horizontal overflow at 600 px.

#### Scenario: Header at 600 px
- **WHEN** the page is 600 px wide
- **THEN** every header control is visible and the tab bar scrolls

### Requirement: Vertical layout
At 640 px or less, the dashboard page SHALL stack fixed-column rows in one column and SHALL show History rows as cards.

#### Scenario: Page at 400 px
- **WHEN** the page is 400 px wide
- **THEN** no visible element of the active tab ends right of the window
- **AND** each History cell is on its own line after its label

#### Scenario: Page at 641 px
- **WHEN** the page is 641 px wide or more
- **THEN** the layout is the same as before the change
