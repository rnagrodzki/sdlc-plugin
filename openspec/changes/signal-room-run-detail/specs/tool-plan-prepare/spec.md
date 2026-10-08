# Spec Delta

## ADDED Requirements

### Requirement: Guardrail counts in plan state
The first `plan_prepare` call of a run SHALL store `guardrailCounts` `{total, error, warning}` in the plan state. A guardrail with no severity SHALL count as `error`. A resume call SHALL write nothing. A guardrail load error SHALL store no key.

#### Scenario: New run
- **WHEN** a new plan run loads 2 `error` guardrails and 1 `warning` guardrail
- **THEN** the plan state has `guardrailCounts` `{total:3, error:2, warning:1}`

#### Scenario: Resume
- **WHEN** `plan_prepare` runs with `resume: true`
- **THEN** the plan state file stays byte-identical

#### Scenario: No guardrails
- **WHEN** a new run loads 0 guardrails
- **THEN** the plan state has `guardrailCounts` `{total:0, error:0, warning:0}`
