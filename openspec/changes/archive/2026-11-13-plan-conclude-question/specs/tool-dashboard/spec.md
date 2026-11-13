## ADDED Requirements

### Requirement: Stopped plan run
The dashboard SHALL show a plan run that has `planIntegrity.stopped` and no `done` as status `failed`. The station of the saved step SHALL be `failed`, and the pipeline SHALL have one issue `Stopped by the user at <station>`.

#### Scenario: Stopped at the format gate
- **WHEN** a plan run has saved step `6.6` and `planIntegrity.stopped`
- **THEN** station `finalize` is `failed`
- **AND** the issue is `Stopped by the user at finalize`
- **AND** `completedAt` is the stopped time

#### Scenario: Done and stopped
- **WHEN** a plan run has both `done` and `stopped`
- **THEN** the run shows `completed`
