# Spec Delta

## ADDED Requirements

### Requirement: Stable finding IDs
`merge_results` SHALL give every issue in `allIssues` an `id`: `f-` plus the first 8 hex characters of the SHA-256 of its dedup key (gate ID and lower-case trimmed summary). It SHALL ignore an `id` in the input.

#### Scenario: Same finding twice
- **WHEN** two `merge_results` calls each hold gate `G14` with summary `Missing test`
- **THEN** both issues have the same `id`

#### Scenario: Case and spaces
- **WHEN** one summary is ` Missing Test ` and another is `missing test` on the same gate
- **THEN** both issues have the same `id`

#### Scenario: Input id
- **WHEN** an input issue has `id` `f-00000000`
- **THEN** the output `id` is the computed value
