# Spec Delta

## ADDED Requirements

### Requirement: Guardrail severity downgrade
The `guardrails` action SHALL return one error finding for each candidate in `candidatesJson` that lowers the severity of a disk entry with the same id. A disk entry with no severity counts as `error`.

#### Scenario: Error to warning
- **WHEN** the disk entry `no-ci-bypass` has severity `error` and a candidate with the same id has severity `warning`
- **THEN** the findings include `no-ci-bypass: severity lowered from error to warning (harden is strengthen-only)`
- **AND** the fix is `Keep severity error, or propose a new guardrail id for the weaker rule.`

#### Scenario: No candidates
- **WHEN** `candidatesJson` is empty
- **THEN** the findings are the same as before this change
