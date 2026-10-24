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
| `stepTime` | Set only for `checkpoint`: `{step, iteration, at, status}`; `status` is `added` (this call stored the entry), `kept` (an earlier entry owns the time; `at` is its time) or `not recorded` (see `warnings`; `at` is empty) |

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
The `checkpoint` marker SHALL add `{step, iteration, at}` to the top-level `stepTimes` list of the plan state for the first checkpoint of each step and iteration, and SHALL return `stepTime`. It SHALL keep 64 entries or fewer and SHALL keep every other top-level key. At the limit, or when `stepTimes` is not a list, it SHALL still save the checkpoint, add no entry, return `stepTime.status` `not recorded` and return a warning.

#### Scenario: First checkpoint
- **WHEN** `stepTimes` is absent and a checkpoint sends step `2` and iteration `0`
- **THEN** `stepTimes` holds one entry for step `2` iteration `0`
- **AND** `stepTime.status` is `added` and `stepTime.at` is the stored `at`

#### Scenario: Repeated checkpoint
- **WHEN** two checkpoint calls send step `3` and iteration `1`
- **THEN** `stepTimes` holds one entry for step `3` iteration `1`, with the `at` of the first call
- **AND** the second call returns `stepTime.status` `kept` with the `at` of the first call

#### Scenario: Last free entry
- **WHEN** `stepTimes` holds 63 entries and a checkpoint sends a new step and iteration
- **THEN** `stepTimes` holds 64 entries and the checkpoint returns no warning

#### Scenario: Full list
- **WHEN** `stepTimes` holds 64 entries and a checkpoint sends step `3` and iteration `0`
- **THEN** the checkpoint returns `ok: true`, `stepTime.status` `not recorded`, and the warning `Step time not recorded for step 3 iteration 0: stepTimes holds 64 entries, the limit (64). The Steps line of the Step 7 timing report leaves this step out.`

#### Scenario: Value that is not a list
- **WHEN** `stepTimes` holds a string and a checkpoint sends step `2` and iteration `0`
- **THEN** the checkpoint keeps the value, writes the checkpoint, and returns the warning `Step time not recorded for step 2 iteration 0: stepTimes in <state file> is a string, not a list. Expected a list such as [{"step":"2","iteration":0,"at":"2026-10-23T12:00:00Z"}]. The Step 7 timing report shows Steps: unknown. To fix it, remove the stepTimes key from that file.`
- **AND** a later `done` returns the warning that starts with `Timing: stepTimes in <state file> is a string, not a list, so the Steps line shows unknown.`

### Requirement: Done marker timing report
The `done` marker SHALL return `timing`, a raw-text report with the total time, the time of each step and the 3 slowest agents, and `next` `Print timing verbatim. Then continue Step 7 with the plan-mode or normal-mode branch.` It SHALL return ok when timing data is absent or bad. Each warning SHALL start with `Timing: `. The report SHALL read only the first 64 `stepTimes` entries.

#### Scenario: Report lines
- **WHEN** `plan_mark({marker: "done"})` succeeds
- **THEN** `timing` starts with `Step 7: Timing (wall clock, UTC)` and has the lines `Total:`, `Steps:` and `Slowest agents:`

#### Scenario: No start stamp
- **WHEN** `planIntegrity.skillInvoked` is absent
- **THEN** `timing` holds `Total: unknown`

#### Scenario: Evidence folder cannot be listed
- **WHEN** the evidence folder path is a regular file
- **THEN** `done` returns `ok: true`, a warning that starts with `Timing: cannot read evidence:`, and `Slowest agents: unknown`

#### Scenario: Evidence folder missing
- **WHEN** the evidence folder does not exist
- **THEN** `done` returns `ok: true`, no warning, and `Slowest agents: none`

#### Scenario: Four finished writers
- **WHEN** 4 finished writers have `startedAt`
- **THEN** `timing` lists the 3 longest writers and leaves out the shortest

#### Scenario: Skipped writers
- **WHEN** writer `main` is done, one writer has status `running`, and one writer has no `startedAt`
- **THEN** `timing` lists none of these writers

#### Scenario: Bad writer time
- **WHEN** writer `lens-risk-r1` has an `updatedAt` before its `startedAt`
- **THEN** `done` returns the warning `Timing: writer lens-risk-r1 skipped: updatedAt <updatedAt> is before startedAt <startedAt>`

#### Scenario: Writer time not RFC 3339
- **WHEN** writer `bad-time` has `startedAt` `yesterday`
- **THEN** `done` returns the warning `Timing: writer bad-time skipped: startedAt "yesterday" is not RFC 3339 (example: 2026-10-23T12:00:00Z)`

#### Scenario: Step time before the previous step
- **WHEN** the `stepTimes` entry of step `3` has a time before the entry of the step before it
- **THEN** `timing` shows `3 unknown`
- **AND** `done` returns the warning `Timing: step 3 is shown as unknown: its time <T3> is before <T2>, the time of the step before it`

#### Scenario: List past the limit
- **WHEN** `stepTimes` holds 66 entries
- **THEN** the report reads the first 64 entries only
- **AND** `done` returns the warning `Timing: stepTimes holds 66 entries; the report reads the first 64 only.`
