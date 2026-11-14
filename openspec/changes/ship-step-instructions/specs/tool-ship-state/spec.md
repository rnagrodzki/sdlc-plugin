# Spec Delta

## ADDED Requirements

### Requirement: instructions-next action
The action SHALL take `detail.key`, SHALL mark each running record of the key failed with the outcome `result not recorded, not run again`, then SHALL mark the first pending record running and return it with `remaining`, `runMode` and `failedRunning` (the numbers of the records it marked failed).

#### Scenario: Next record
- **WHEN** the key `pr` has a pending record 2
- **THEN** the action returns `pr #2` and `next` says `Do instruction pr #2, then call instruction-record.`

#### Scenario: No open record
- **WHEN** the key has no pending record
- **THEN** the action returns `(none)` and `next` says `No open instruction for <key>. Continue with the next step.`

#### Scenario: Run mode not boolean
- **WHEN** `flags.auto` is not a boolean
- **THEN** `runMode` is `--auto` and `warnings` has `flags.auto of the ship state is <type> <value>, want a boolean. Run mode --auto is used. Start a new run to set flags.auto again.`

### Requirement: instruction-record action
The action SHALL take `detail.key`, `detail.instructionStatus` (done, failed, not run), and `detail.instructionNumber` or `detail.instructionAll: true`, with `detail.instructionOutcome` (required for failed and not run, trimmed, at most 1000 characters). A closed record SHALL NOT change. With `instructionAll: true` the status SHALL be `not run`: a pending record becomes `not run` with the outcome, and a running record becomes `failed` with the outcome `result not recorded, not run again`. The output SHALL return `key` and `closed`, one entry for each closed record with `number`, `status`, `outcome` and `endedAt`.

Record states:

```mermaid
stateDiagram-v2
  [*] --> pending: seed
  pending --> running: instructions-next
  running --> done: instruction-record
  running --> failed: instruction-record or next call
  running --> notrun: instruction-record
  pending --> notrun: sweep
```

#### Scenario: Closed record
- **WHEN** the record is `done`
- **THEN** the action returns DomainError `instruction pr #1 is closed (done)`

#### Scenario: Outcome required
- **WHEN** the status is `failed` and the outcome is blank
- **THEN** the action returns DomainError `outcome is required for failed and not run`

#### Scenario: Record key with another action
- **WHEN** `complete-step` gets `detail.instructionNumber`
- **THEN** the action returns DomainError `detail.instructionNumber is valid only with instruction-record`

### Requirement: Record sweeps
The `fail`, `skip` and `cleanup-pipeline` actions SHALL close the open records. A pending record becomes `not run` with the outcome `step failed or stopped`, `step skipped` or `step did not run`. A running record becomes `failed` with the outcome `result not recorded, not run again`. A damaged list SHALL close no record: the action SHALL add one warning that starts with `Instruction records not closed:` and SHALL still complete.

#### Scenario: Skip
- **WHEN** the harden step is skipped
- **THEN** each open harden record is `not run` with the outcome `step skipped`

### Requirement: Open records keep the run in flight
A started run that is not completed SHALL count as in flight when a step blocks, a record is pending or running, or the list is damaged.

#### Scenario: End list open
- **WHEN** every step is done and a `ship` record is pending
- **THEN** the run is in flight

#### Scenario: Fresh run
- **WHEN** no step started
- **THEN** the run is not in flight

### Requirement: Commit-check push
When `commit-check` completes the commit step itself, it SHALL push the branch, best effort, and SHALL add any push warning to `warnings` with no change to `stepCompleted`.

#### Scenario: Nothing to commit
- **WHEN** the tree is clean
- **THEN** `stepCompleted` is true and `next` says `Commit step completed. Skip 7c2 and 7d. Run the 7g instruction loop for key commit, then go to the next step.`
