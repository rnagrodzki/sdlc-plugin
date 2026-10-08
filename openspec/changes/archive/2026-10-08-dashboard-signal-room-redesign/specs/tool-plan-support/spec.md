# Spec Delta

## ADDED Requirements

### Requirement: merge_results blocking count
The `merge_results` action SHALL return `blockingCount`: the number of blocking issues after the merge, `0` too. The value SHALL equal the count in its summary text. Other actions SHALL not return `blockingCount`.

#### Scenario: Two blocking issues
- **WHEN** `merge_results` merges lens results with 2 blocking issues and 1 advisory issue
- **THEN** the output has `blockingCount: 2`

#### Scenario: No blocking issues
- **WHEN** `merge_results` merges lens results with no blocking issue
- **THEN** the output has `blockingCount: 0` and `mergedStatus: "Approved"`

#### Scenario: Other action
- **WHEN** the call uses `action: "material_snapshot"`
- **THEN** the output has no `blockingCount`
