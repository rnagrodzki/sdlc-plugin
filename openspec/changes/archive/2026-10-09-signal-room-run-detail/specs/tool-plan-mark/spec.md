# Spec Delta

## ADDED Requirements

### Requirement: Round finding IDs
The `review-round` marker SHALL accept an optional `findings` list of at most 200 `{id, fixed}` entries, where `id` matches `^f-[0-9a-f]{8}$`. A passed empty list SHALL be stored as `[]`. An absent key SHALL not be stored. A bad entry SHALL return a DomainError with a Suggestion and write nothing.

#### Scenario: Round with IDs
- **WHEN** `review-round` gets `findings` `[{id:"f-3a9c1e07", fixed:true}]`
- **THEN** the stored round has that `findings` list

#### Scenario: Old caller
- **WHEN** `review-round` gets no `findings` key
- **THEN** the stored round has no `findings` key

#### Scenario: Bad ID
- **WHEN** a finding has `id` `x1`
- **THEN** the call returns a DomainError with the Suggestion `Pass the id from merge_results allIssues.`

### Requirement: Review outcome marker
The `review-outcome` marker SHALL replace `reviewOutcome` with the full list of each call: `{findings:[{id, text, choice, reason}]}`, with `choice` `accepted`, `rejected`, or `stop`, `text` and `reason` at most 200 characters, and at most 200 findings. A bad input SHALL return a DomainError with a Suggestion and write nothing. The result SHALL set `next`.

#### Scenario: Second call replaces
- **WHEN** call 1 stores findings `a` and `b`
- **AND** call 2 passes only `a`
- **THEN** `reviewOutcome` holds only `a`

#### Scenario: Bad choice
- **WHEN** a finding has `choice` `maybe`
- **THEN** the call returns a DomainError with the Suggestion `Use accepted, rejected, or stop.`

#### Scenario: Empty list
- **WHEN** `data.findings` is empty
- **THEN** the call returns a DomainError
- **AND** the stored outcome does not change
