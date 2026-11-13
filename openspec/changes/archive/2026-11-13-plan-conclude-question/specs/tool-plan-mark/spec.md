## ADDED Requirements

### Requirement: Stopped marker
The `stopped` marker SHALL set `planIntegrity.stopped` to the current UTC time (RFC 3339) and SHALL ignore `data`. A run with a `stopped` key SHALL not count as active.

| Run state | Marker | Result |
|---|---|---|
| active | `stopped` | stamp the time |
| stopped | `stopped` | keep the first time |
| done | `stopped` | DomainError `plan run <runId> is done; stopped not recorded` |
| stopped | `done` | DomainError `plan run <runId> is stopped; done not recorded` |

#### Scenario: Stop an active run
- **WHEN** `plan_mark({marker: "stopped", runId: "plan-main-20261113T155524Z"})` succeeds
- **THEN** `planIntegrity.stopped` holds a timestamp
- **AND** `next` is `Run stopped. Print the plan file path and the step. End the plan. Do not hand off the plan.`

#### Scenario: Second stop
- **WHEN** `stopped` is called on a run that already has `planIntegrity.stopped`
- **THEN** the stored time does not change
- **AND** the call writes nothing

#### Scenario: Stop result is echoed
- **WHEN** a `stopped` call stamps the time
- **THEN** the result has `stopped` `{at, status}` with `status` `stamped` and `at` the stored time
- **AND** a repeat call returns `status` `kept` with `at` the first time

#### Scenario: Done after stop
- **WHEN** `done` is called on a stopped run
- **THEN** the call returns a DomainError with the Suggestion `The user stopped this run. Start a new plan run.`

### Requirement: Review outcome stop records the stop
A `review-outcome` call with at least one `stop` choice SHALL store the outcome and stamp `planIntegrity.stopped` in the same state write. The result SHALL carry `stopped` `{at, status}`, where `status` is `stamped`, `kept` or `not recorded`.

#### Scenario: Stop answer on an active run
- **WHEN** `review-outcome` has a finding with `choice: "stop"` on an active run
- **THEN** `reviewOutcome` and `planIntegrity.stopped` are both stored
- **AND** the result has `stopped.status` `stamped`
- **AND** `next` is `Outcome stored. The run is stopped. Offer harden when interactive, then end the run and report the open findings. Do not hand off the plan.`

#### Scenario: Stop answer on a done run
- **WHEN** `review-outcome` has a `stop` choice on a done run
- **THEN** the outcome is stored with no `stopped` stamp
- **AND** the result has the warning `plan run <runId> is done; stopped not recorded`
- **AND** the result has `stopped.status` `not recorded`
- **AND** `next` is `Outcome stored. The run is done, so stopped is not recorded. Report the open findings. Do not hand off the plan again.`
