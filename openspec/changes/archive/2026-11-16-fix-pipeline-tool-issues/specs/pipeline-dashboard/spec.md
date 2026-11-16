# Spec Delta

## ADDED Requirements

### Requirement: Review queue keeps one row per finding
When the review queue has rows, it SHALL add one `open` row for each review finding whose `file:line` has no row. When it adds 1 or more rows, `unrouted` SHALL be 0 and `reason` SHALL be `""`.

#### Scenario: First fix row
- **WHEN** review found 5 findings at 5 different lines and 1 `queued` fix row exists for one of them
- **THEN** the queue has 1 `queued` row and 4 `open` rows

#### Scenario: Finding with no line
- **WHEN** a finding has no `file` or no `line`
- **THEN** the queue adds no row for it
