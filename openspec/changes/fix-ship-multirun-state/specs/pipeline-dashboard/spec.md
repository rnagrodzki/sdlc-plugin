# Spec Delta

## MODIFIED Requirements

### Requirement: Review queue row status
Queue rows SHALL come from the fix records, then from `deferredFindings`, with the fix status as the row status. A deferred finding with reason `below-threshold` or no reason SHALL get status `below`, and any other reason SHALL get `deferred`. With no fix record and no deferred finding, the rows SHALL be the review findings with status `open`. A deferred finding SHALL get no row of its own when a fix record pairs with it: the same `deferredId`, else the same file, line and title, else (only for a deferred finding with no `deferredId`) the only fix record of any status on that file and line.

#### Scenario: Deferred twin of a fix
- **WHEN** a deferred finding has the same file, line and title as a fix record
- **THEN** the queue has one row for the fix record only

#### Scenario: Below threshold
- **WHEN** a deferred finding has reason `below-threshold`
- **THEN** its row has status `below`

#### Scenario: Disagree
- **WHEN** a deferred finding has reason `disagree`
- **THEN** its row has status `deferred`

#### Scenario: Before the first fix record
- **WHEN** the ship state has no fix record and no deferred finding, and review reported 2 findings
- **THEN** the queue has 2 rows with status `open`

#### Scenario: Same deferred id
- **WHEN** a deferred finding and a fix record have the same `deferredId` and other titles
- **THEN** the queue has one row for the fix record only

#### Scenario: Changed title, one fix record on the line
- **WHEN** a deferred finding with no `deferredId` has another title than the only fix record on the same file and line
- **THEN** the queue has one row for the fix record only

#### Scenario: Deferred id that no fix record has, one fix record on the line
- **WHEN** a deferred finding has a `deferredId` that no fix record has, and another title than the only fix record on the same file and line
- **THEN** the queue has a row for the fix record and a row for the deferred finding

#### Scenario: Two fix records on the line
- **WHEN** a deferred finding has another title than each of two fix records on the same file and line
- **THEN** the queue has a row for each fix record and a row for the deferred finding

#### Scenario: Empty deferred id
- **WHEN** a deferred finding has no `deferredId` and a fix record has no `deferredId`
- **THEN** the empty values do not pair them
