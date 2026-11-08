# Spec Delta

## ADDED Requirements

### Requirement: Handoff validation adds the OpenSpec rule
The skill SHALL pass `handoff: true` in the Step 6.6 `plan_format` call and in the rejected-handoff rerun, and SHALL NOT pass it in the Step 2 format pre-check. A PF15 finding stops the run before Step 7, so the skill does not call ExitPlanMode.

#### Scenario: Unauthored change at Step 6.6
- **WHEN** the Step 6.6 `plan_format` call returns a PF15 finding
- **THEN** the skill shows the finding and its `fix`
- **AND** the skill does not call ExitPlanMode

#### Scenario: Format pre-check
- **WHEN** the Step 2 format pre-check runs on a plan with `**OpenSpec-Create:**`
- **THEN** the call has no `handoff` field and returns no PF15 finding
