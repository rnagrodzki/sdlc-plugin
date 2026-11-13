## ADDED Requirements

### Requirement: Stopped run reuse
A `plan_prepare` call that names a stopped run SHALL return DomainError `plan run <runId> is stopped` with a Suggestion to start a new plan run, and SHALL write nothing.

#### Scenario: Resume of a stopped run
- **WHEN** `plan_prepare({resume: true, runId: "plan-main-20261113T155524Z"})` names a run with `planIntegrity.stopped`
- **THEN** the call returns DomainError `plan run plan-main-20261113T155524Z is stopped`

#### Scenario: No run ID
- **WHEN** `plan_prepare` is called with no run ID and the newest run of the branch is stopped
- **THEN** the call does not reuse that run
