## ADDED Requirements

### Requirement: Conclude question
The skill SHALL ask one conclude question, "Is this all? Conclude the plan?", with the options Conclude (Recommended), Change more and Stop. Before it asks, it SHALL restate the current step, the steps that are left, the open findings and the plan file path.

#### Scenario: Free-text answer
- **WHEN** the user answers the conclude question with free text
- **THEN** the skill applies the text as one more change
- **AND** the skill asks the conclude question again

#### Scenario: Auto mode
- **WHEN** `--auto` is set
- **THEN** the skill takes Conclude with no question

### Requirement: Conclude question points
The skill SHALL ask the conclude question at three points only: a user message before the done marker, findings at Step 6.5 or Step 6.6, and a change request after the Step 7 handoff.

#### Scenario: Mid-run message, Conclude
- **WHEN** a user message in Steps 1 to 7 before the done marker answers no skill question
- **AND** the user picks Conclude
- **THEN** the skill continues at the saved step and runs every step that is left

#### Scenario: Gate findings, Conclude
- **WHEN** Step 6.6 returns findings and the user picks Conclude
- **THEN** the skill fixes the findings, runs Step 6.6 again and counts one try

#### Scenario: Gate findings, Change more
- **WHEN** a gate has findings and the user picks Change more
- **THEN** the skill applies the next change and runs Step 6.5 again

#### Scenario: After handoff, Stop
- **WHEN** the user asks for a change after the handoff and then picks Stop
- **THEN** the skill ends with no state call and the plan file stays as it is

#### Scenario: Message during gate findings
- **WHEN** a user message arrives while a gate has findings
- **THEN** the skill uses the mid-run point
- **AND** the question lists the gate findings as open findings

### Requirement: Gate fix limit
At a gate, the skill SHALL make at most 3 fix tries for each gate and SHALL store the count, so a resume after a compaction keeps it.

#### Scenario: Third try fails in an interactive run
- **WHEN** the gate still has findings after 3 tries
- **THEN** the conclude question has no Conclude option
- **AND** it offers only Change more and Stop

#### Scenario: Third try fails under auto
- **WHEN** `--auto` is set and the gate still has findings after 3 tries
- **THEN** the skill takes Stop and prints the findings

### Requirement: Conclude stop rule
When the user picks Stop before the done marker, the skill SHALL call `plan_mark` with marker `stopped` and the run ID, SHALL stop each Step 1 explorer that still runs, and SHALL print the plan file path and the step.

#### Scenario: Explorer not stopped
- **WHEN** `TaskStop` fails for an explorer
- **THEN** the skill prints `Warning: <writerId> not stopped, it can still run.`
- **AND** the skill adds the writer ID to `notStopped`

#### Scenario: Marker call fails
- **WHEN** the `stopped` call returns an error
- **THEN** the skill prints the error and ends the plan

## MODIFIED Requirements

### Requirement: Hard gates before handoff
The skill SHALL pass two hard gates before Step 7. On failure it SHALL show the findings verbatim and ask the conclude question at the Gate point. It SHALL not go to Step 7 with findings.

| Gate | Call | Fails when |
|---|---|---|
| Step 6.5 links | `links_validate({file: <plan>, offline: false})` | Any result `status` is not `ok` |
| Step 6.6 format | `validate({action: "plan_format", file: <plan>, final, template})` | `findings` is not empty |

- Full pipeline: `final: true` and `template` set to `template.activeTemplatePath`.
- Lightweight: `final: false` and no `template`.

#### Scenario: Broken link
- **WHEN** `links_validate` returns a result with status other than `ok`
- **THEN** the skill lists the violations and asks the conclude question before Step 7

### Requirement: OpenSpec files authored again after rejected handoff
When the user rejects the plan-mode handoff with feedback, or replies with a change in normal mode, the skill SHALL change the plan for the feedback and ask the conclude question. On Conclude, a plan with an `**OpenSpec-Staging:**` line SHALL author all OpenSpec files again from the changed plan file and stage them with all files before the next handoff.

- The skill reads and writes no plan run state on this path. It makes no `plan_mark` call and no `evidence_record` call.
- It calls `links_validate` and `validate` with action `plan_format` and `final: true` before the next ExitPlanMode.
- The `openspec_stage` attempt counter starts at 0, with the same 5-attempt rule.
- The `## OpenSpec Appendix` table is replaced in full from the new files.

#### Scenario: Feedback changes a requirement
- **WHEN** the user rejects ExitPlanMode with feedback that removes a plan task
- **THEN** the staged `tasks.md` no longer lists that task
- **AND** the skill calls ExitPlanMode again

#### Scenario: Plan without staging
- **WHEN** the user rejects ExitPlanMode and the plan header has no `**OpenSpec-Staging:**` line
- **THEN** the skill calls no `openspec_*` action
- **AND** the skill asks the conclude question
