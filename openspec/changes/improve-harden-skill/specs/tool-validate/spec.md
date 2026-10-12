# Spec Delta

## MODIFIED Requirements

### Requirement: Guardrail severity downgrade
The `guardrails` action SHALL return one error finding for each candidate in `candidatesJson` that lowers the severity of a disk entry with the same id. A disk entry with no severity counts as `error`.

#### Scenario: Error to warning
- **WHEN** the disk entry `no-ci-bypass` has severity `error` and a candidate with the same id has severity `warning`
- **THEN** the findings include `no-ci-bypass: severity lowered from error to warning (harden never lowers a severity)`
- **AND** the fix is `Keep severity error, or propose a new guardrail id for the weaker rule.`

#### Scenario: No candidates
- **WHEN** `candidatesJson` is empty
- **THEN** the findings are the same as before this change

## ADDED Requirements

### Requirement: Guardrail check scope
The `guardrails` action SHALL report findings only for the ids in `scopeIds` when it is set, else for the candidate ids when `candidatesJson` is set, else for every id. Severity-downgrade findings of candidates SHALL always be reported.

#### Scenario: Old finding outside the scope
- **WHEN** the section holds an oversize entry `old`
- **AND** `candidatesJson` holds one clean entry `new`
- **THEN** the findings are empty

#### Scenario: Empty scope
- **WHEN** `scopeIds` is `[]`
- **THEN** the tool returns a DomainError `scopeIds is empty`

#### Scenario: Unknown scope id
- **WHEN** `scopeIds` names `x` and `x` is not on disk and not in the candidates
- **THEN** the tool returns a DomainError `scopeIds names x, which is not in section plan`

### Requirement: Remove candidates
A candidate `{"id":"x","remove":true}` SHALL drop disk entry `x` from the in-memory check. A remove candidate SHALL set no other field, and entry `x` SHALL exist on disk.

#### Scenario: Remove an entry
- **WHEN** the disk has oversize entry `x`
- **AND** `candidatesJson` is `[{"id":"x","remove":true}]`
- **THEN** the findings are empty

#### Scenario: Remove a missing entry
- **WHEN** `candidatesJson` is `[{"id":"y","remove":true}]` and no entry `y` is on disk
- **THEN** the tool returns a DomainError `remove candidate y has no entry on disk`

### Requirement: Dimensions file scope
The `dimensions` action with `file` SHALL report the findings of the dimension file whose base name matches, plus the cross-file duplicate-name findings that name it. A `file` that matches no dimension file SHALL return a DomainError.

#### Scenario: One file
- **WHEN** `file` is `.sdlc-v2/review-dimensions/a.md` and `b.md` has a finding
- **THEN** the findings hold no entry for `b.md`

#### Scenario: No match
- **WHEN** `file` is `missing.md`
- **THEN** the tool returns a DomainError `file missing.md matches no review-dimension file`
