# Spec Delta

## ADDED Requirements

### Requirement: Findings with a deferred id
For a finding with a `**Deferred-id:** <id>` line, the skill SHALL add `deferredId:<id>` to its `healing_record` kind `fixed` call when the finding is fixed, and SHALL make no `defer` call when it is not fixed.

- An unfixed finding with that line counts as recorded for the `fix-progress` `deferred` call.

#### Scenario: Fixed fix-pass finding
- **WHEN** a finding has `**Deferred-id:** review-deferred-x-2` and the fix is verified
- **THEN** the skill calls `healing_record` with kind `fixed` and `deferredId` `review-deferred-x-2`

#### Scenario: Unfixed fix-pass finding
- **WHEN** a finding has `**Deferred-id:** review-deferred-x-2` and it is not fixed
- **THEN** the skill makes no `defer` call
- **AND** the reply says `stays deferred as review-deferred-x-2`
