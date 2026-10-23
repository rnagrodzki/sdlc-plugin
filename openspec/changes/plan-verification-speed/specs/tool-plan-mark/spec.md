# Spec Delta

## MODIFIED Requirements

### Requirement: Input and output fields
The tool SHALL accept the input fields below and SHALL return the output fields below on success.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `marker` | string enum | yes | plain text | One of `plan-file`, `skillInvoked`, `guardrailsEvaluated`, `critiqueRan`, `done`, `guardrailResults`, `criticalDecisions`, `checkpoint`, `review-round`, `review-outcome` |
| `path` | string | only for `plan-file` | plain text | Plan document path to record |
| `data` | object | only for `checkpoint` and `review-round` | JSON | Payload for `guardrailResults`, `criticalDecisions`, `checkpoint` and `review-round`; ignored otherwise |

| Field | Meaning |
|---|---|
| `ok` | `true` on success |
| `marker` | The marker that was written |
| `path` | Path of the plan state file that was written |
| `next` | Set only for `checkpoint`, `review-outcome` and `done` |
| `warnings` | `checkpoint`: the step time was not recorded; `done`: the history append failed, or the timing report skipped data |
| `timing` | Set only for `done`: the Step 7 timing report as raw text |

#### Scenario: Successful timestamp marker
- **WHEN** `plan_mark({marker: "critiqueRan"})` succeeds
- **THEN** the output has `ok: true`, `marker: "critiqueRan"` and `path` set to the plan state file
- **AND** `next` is empty

### Requirement: Checkpoint next text
The `checkpoint` marker SHALL return `next` as shown below. The tool SHALL read the `[planStyle]` config section fresh on every checkpoint call to build it.

| Condition | `next` text |
|---|---|
| Always | `Checkpoint saved at step <s>. Continue step <s>.` |
| `step` is `5` and `iteration` equals the review-loop limit (5) | append ` This is review round 5 of 5, the last round. If blocking issues remain after it, ask the user with AskUserQuestion; do not start round 6.` |
| `[planStyle].instructions` has entries | append a new line `Custom plan instructions (follow them in this step):`, then one line `<n>. <instruction>` per entry |
| `[planStyle]` cannot be read | append ` Warning: Failed to read planStyle config: <cause> — custom plan instructions could not be loaded; fix local.toml.` |

#### Scenario: Instructions added between calls
- **WHEN** `.sdlc-v2/local.toml` gains two `[planStyle].instructions` entries `A` and `B` after a first checkpoint call
- **THEN** the next checkpoint call's `next` ends with `Custom plan instructions (follow them in this step):\n1. A\n2. B`

#### Scenario: Last review round
- **WHEN** `plan_mark({marker: "checkpoint", data: {step: "5", iteration: 5}})` succeeds
- **THEN** `next` contains `This is review round 5 of 5, the last round.`

#### Scenario: Earlier review round
- **WHEN** `plan_mark({marker: "checkpoint", data: {step: "5", iteration: 4}})` succeeds
- **THEN** `next` does not contain `the last round`

#### Scenario: Non-checkpoint marker
- **WHEN** `plan_mark({marker: "critiqueRan"})` succeeds
- **THEN** `next` is empty

## ADDED Requirements

### Requirement: Step times on checkpoint
The `checkpoint` marker SHALL add `{step, iteration, at}` to the top-level `stepTimes` list of the plan state for the first checkpoint of each step and iteration. It SHALL keep 64 entries or fewer and SHALL keep every other top-level key.

#### Scenario: First checkpoint
- **WHEN** `stepTimes` is absent and a checkpoint sends step `2` and iteration `0`
- **THEN** `stepTimes` holds one entry for step `2` iteration `0`

#### Scenario: Repeated checkpoint
- **WHEN** two checkpoint calls send step `3` and iteration `1`
- **THEN** `stepTimes` holds one entry for step `3` iteration `1`, with the `at` of the first call

#### Scenario: Last free entry
- **WHEN** `stepTimes` holds 63 entries and a checkpoint sends a new step and iteration
- **THEN** `stepTimes` holds 64 entries and the checkpoint returns no warning

#### Scenario: Full list
- **WHEN** `stepTimes` holds 64 entries and a checkpoint sends a new step and iteration
- **THEN** the checkpoint returns `ok: true` and the warning `step time not recorded: 64 entries stored`

#### Scenario: Value that is not a list
- **WHEN** `stepTimes` holds a string
- **THEN** the checkpoint keeps the value, writes the checkpoint, and returns the warning `stepTimes is not a list: step time not recorded`

### Requirement: Done marker timing report
The `done` marker SHALL return `timing`, a raw-text report with the total time, the time of each step and the 3 slowest agents, and `next` `Print timing verbatim. Then continue Step 7 with the plan-mode or normal-mode branch.` It SHALL return ok when timing data is absent or bad.

#### Scenario: Report lines
- **WHEN** `plan_mark({marker: "done"})` succeeds
- **THEN** `timing` starts with `Step 7: Timing (wall clock, UTC)` and has the lines `Total:`, `Steps:` and `Slowest agents:`

#### Scenario: No start stamp
- **WHEN** `planIntegrity.skillInvoked` is absent
- **THEN** `timing` holds `Total: unknown`

#### Scenario: Evidence folder cannot be listed
- **WHEN** the evidence folder path is a regular file
- **THEN** `done` returns `ok: true`, a warning that starts with `timing: cannot read evidence:`, and `Slowest agents: unknown`

#### Scenario: Four finished writers
- **WHEN** 4 finished writers have `startedAt`
- **THEN** `timing` lists the 3 longest writers and leaves out the shortest

#### Scenario: Skipped writers
- **WHEN** writer `main` is done, one writer has status `running`, and one writer has no `startedAt`
- **THEN** `timing` lists none of these writers

#### Scenario: Bad writer time
- **WHEN** writer `lens-risk-r1` has an `updatedAt` before its `startedAt`
- **THEN** `done` returns the warning `timing: writer lens-risk-r1 skipped: end before start`
