# Spec Delta

## ADDED Requirements

### Requirement: init receives the wave plan
The `execute_state` `init` call SHALL pass `plannedWavesJson` with the final wave schedule that Step 4 confirms: wave numbers and task IDs only, with pre-wave tasks as wave `0`.

#### Scenario: Confirmed schedule
- **WHEN** Step 4 confirms wave 1 with tasks `1`, `2` and wave 2 with task `3`
- **THEN** `init` gets `plannedWavesJson` `[{"number":1,"taskIds":["1","2"]},{"number":2,"taskIds":["3"]}]`
