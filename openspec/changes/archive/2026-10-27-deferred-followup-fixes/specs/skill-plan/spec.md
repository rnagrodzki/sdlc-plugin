# Spec Delta

## MODIFIED Requirements

### Requirement: Template resolution and routing
After the gate check and routing, the skill SHALL call `plan_prepare` again with `resolveTemplate: true`, the `runId` of the first call, `fromOpenspecDirect`, `openspecInlineGenerate`, `lightweight`, `fileCount`, and the same first-call fields. It SHALL write `template.headerMarkdown` + `template.skeletonMarkdown` to the plan file.

| Scope | Normal mode | Plan mode |
|---|---|---|
| 1 file, clear change | Stop — tell the user no plan is needed | Lightweight plan |
| 2–3 files | Lightweight: no explorer fan-out, no Step 5 | Lightweight |
| 4+ files or unclear | Full pipeline | Full pipeline |
| Independent subsystems | Split into separate plans | Split |

- `template.routing.pipelineMode` (`full`, `lightweight`, `skip`) selects the branch; the "no plan needed" and "split" decisions stay with the skill.
- In Step 2, when requirements span independent subsystems, the skill asks with AskUserQuestion whether to proceed as one plan or split into N plans, and waits for the answer. `--auto` does not suppress this question.

#### Scenario: Lightweight routing
- **WHEN** `template.routing.pipelineMode` is `lightweight`
- **THEN** the skill skips explorer dispatch and the Step 5 review loop

#### Scenario: Template call names its run
- **WHEN** the first `plan_prepare` call returned `runId` `plan-main-20261026T084445Z`
- **THEN** the template-resolution call passes `runId: "plan-main-20261026T084445Z"`

### Requirement: Checkpoints and integrity markers
The skill SHALL record progress with `plan_mark`, one call at a time, and SHALL pass the `runId` of this session in each call.

| When | Call |
|---|---|
| Step 0, after the `plan-file` marker | `plan_mark({marker: "checkpoint", runId: "<runId>", path: "", data: {step: "0", iteration: 0}})` |
| Start of Step 1, before the path choice, on every path | `plan_mark({marker: "checkpoint", runId: "<runId>", path: "", data: {step: "1", iteration: 0}})` |
| Start of Steps 2, 3, 4, 5, 6, 6.5, 6.6, 7 | `plan_mark({marker: "checkpoint", runId: "<runId>", path: "", data: {step, iteration}})` |
| Fan-outs (Step 1 explorers, Step 3 lanes, Step 5 lenses or reviewer) | same, plus `expectedWriters` with the agent writers only, never the gate checks |
| After the `guardrail-compliance` lane result is merged | `plan_mark({marker: "guardrailsEvaluated", runId: "<runId>"})` |
| After every lane and both gate checks returned and merged | `plan_mark({marker: "critiqueRan", runId: "<runId>"})` |
| Before the Step 7 handoff branch | `plan_mark({marker: "done", runId: "<runId>"})` |

- `guardrailsEvaluated` and `critiqueRan` are written once per run, never on a re-dispatch.
- Steps 0–2 use `iteration: 0`; the first Step 3 and Step 5 rounds are `r1`.
- On a resume at step 1, a step 1 checkpoint with no `expectedWriters` means that no explorer started. The skill then runs Step 1 from its start.

#### Scenario: Critique marker waits for all lanes
- **WHEN** four of the five Step 3 lanes have returned
- **THEN** the skill has not called `plan_mark({marker: "critiqueRan", runId: "<runId>"})`

#### Scenario: Resume before the explorer dispatch
- **WHEN** the session resumes at step 1
- **AND** the step 1 checkpoint has no `expectedWriters`
- **THEN** the skill runs Step 1 from its start

#### Scenario: Done marker names its run
- **WHEN** the skill reaches the Step 7 handoff with `runId` `plan-main-20261026T084445Z`
- **THEN** it calls `plan_mark({marker: "done", runId: "plan-main-20261026T084445Z"})`

### Requirement: Post-compact resume
When the session context has an `Active plan (post-compact):` line whose plan file equals the designated plan file, the skill SHALL resume instead of starting over, with the `runId` of the `Resume with:` line of that banner.

| Condition | Action |
|---|---|
| No line, or plan file differs | Normal Step 0 |
| Match, `step 0` | Redo gate check and routing; call `plan_prepare({resume: true, resolveTemplate: true, skipConfigCheck: true, runId: "<runId of the Resume with: line>", …})` |
| Match, step 1 or later | `plan_prepare({resume: true, resolveTemplate: true, skipConfigCheck: true, runId: "<runId of the Resume with: line>"})`, then `plan_support({action: "evidence_digest", runId})`, then continue at `checkpoint.step` and `checkpoint.iteration` |
| `plan_prepare` returns `no active plan run`, `plan run <runId> not found` or `plan run <runId> is done` | Print `No active plan run to resume — starting a new plan.` and run normal Step 0 |
| `evidence_digest` errors | Print the error; stop; tell the user to re-invoke `/sdlc:plan` |

- The template is written only when the plan file is empty.
- At step 1, explorers marked `done` are kept; missing or stalled ones are not re-dispatched.
- After the resume, every later `plan_mark` call passes the same `runId`.

#### Scenario: No run to resume
- **WHEN** the resume `plan_prepare` call returns `no active plan run`
- **THEN** the skill prints `No active plan run to resume — starting a new plan.`
- **AND** runs Step 0 normally

#### Scenario: Resumed run was deleted
- **WHEN** the resume `plan_prepare` call returns `plan run <runId> not found`
- **THEN** the skill prints `No active plan run to resume — starting a new plan.`
- **AND** runs Step 0 normally

#### Scenario: Resume uses the banner run
- **WHEN** the banner has `Resume with: plan_prepare({resume:true, resolveTemplate:true, skipConfigCheck:true, runId:"plan-main-20261026T084445Z"})`
- **THEN** the skill calls `plan_prepare` with `runId: "plan-main-20261026T084445Z"`

## ADDED Requirements

### Requirement: Run ID on every plan call
The skill SHALL pass the `runId` of this session in every `plan_mark` call and in every `plan_prepare` call with `resume` or `resolveTemplate`, also for the calls that other requirements of this spec show in short form.

- The first `plan_prepare` call has no `runId`: it creates the run.
- The `runId` comes from the first call result, or after a compaction from the `Resume with:` line.
- The `plan-file` marker call keeps its rule: its errors are ignored.

#### Scenario: Mid-run run not found
- **WHEN** a `plan_mark` call returns `plan run <runId> not found` in the middle of a run
- **THEN** the skill tells the user that the run state is gone
- **AND** starts a new plan with `plan_prepare` and no `runId`

#### Scenario: Second session on the same branch
- **WHEN** another plan session starts a newer run on the same branch
- **THEN** each `plan_mark` call of this session still writes to the run of this session
