# Tasks

Group dependencies (each group waits for the group before it):

```mermaid
flowchart LR
  G1["1. Go gate checks and timing"] --> G2["2. Sub-agent prompts"]
  G2 --> G3["3. Skill flow and docs"]
```

## 1. Go gate checks and timing

- [ ] 1.1 Add the `plan_gates` action, the shared Files parser and the 8 MiB read limit in internal/tools/plan_gates.go and internal/tools/validators.go (plan Task 1) — verify: go test -tags integration ./internal/tools/ -run 'TestPlanGates_|TestValidateRoot|TestValidateIn|TestValidatePlanFormat|TestValidatePlanStyle|TestExecActionWaveCompute|TestExecuteState_WaveCompute' <!-- ref:1-1-add-the-plan-gates-action-the-shared-0282d9 -->
- [ ] 1.2 Stamp the writer `startedAt` in internal/tools/plan_evidence.go (plan Task 2) — verify: go test -tags integration ./internal/tools/ -run 'TestEvidence|TestPlanExploreSummary' <!-- ref:1-2-stamp-the-writer-startedat-in-intern-9c496e -->
- [ ] 1.3 Remove the file-existence lane and add `gateChecks` in internal/tools/plan.go (plan Task 3) — verify: go test -tags integration ./internal/tools/ -run 'TestPlanPrepare' && go test -tags integration ./internal/skillcheck/... <!-- ref:1-3-remove-the-file-existence-lane-and-a-8d8a92 -->
- [ ] 1.4 Add `stepTimes` and the `done` timing report in internal/tools/plan_timing.go (plan Task 4) — verify: go test -tags integration ./internal/tools/ -run 'TestPlanTiming_|TestPlanMark|TestDashboardPlan|TestShipReport' && go test -tags integration ./internal/hooks/ -run 'TestStopPlanIntegrity|TestPipelineResumePhase_Plan' <!-- ref:1-4-add-steptimes-and-the-done-timing-re-98227b -->

## 2. Sub-agent prompts

- [ ] 2.1 Drop the Go and inline gates in plugins/sdlc/skills/plan/lane-static-structural-prompt.md and plugins/sdlc/skills/plan/lane-content-coverage-prompt.md (plan Task 5) — verify: go test -tags integration ./internal/skillcheck/... <!-- ref:2-1-drop-the-go-and-inline-gates-in-plug-63479b -->
- [ ] 2.2 Set the 8-task limit and the opposite model in plugins/sdlc/skills/plan/plan-reviewer-prompt.md and plugins/sdlc/skills/plan/lens-*-prompt.md (plan Task 6) — verify: go test -tags integration ./internal/skillcheck/... <!-- ref:2-2-set-the-8-task-limit-and-the-opposit-90d485 -->

## 3. Skill flow and docs

- [ ] 3.1 Use the gate checks and the lane names in Steps 3 and 4 of plugins/sdlc/skills/plan/SKILL.md (plan Task 7) — verify: go test -tags integration ./internal/skillcheck/... <!-- ref:3-1-use-the-gate-checks-and-the-lane-nam-edef4d -->
- [ ] 3.2 Skip the G17 lane after round 1 and use the 8-task limit in Step 5 of plugins/sdlc/skills/plan/SKILL.md (plan Task 8) — verify: go test -tags integration ./internal/skillcheck/... <!-- ref:3-2-skip-the-g17-lane-after-round-1-and-8796d5 -->
- [ ] 3.3 Add the Step 1 start checkpoint and the Step 7 timing print in plugins/sdlc/skills/plan/SKILL.md (plan Task 9) — verify: go test -tags integration ./internal/skillcheck/... <!-- ref:3-3-add-the-step-1-start-checkpoint-and-af5060 -->
- [ ] 3.4 Update the lanes, the limits and the timing docs in docs/plan-architecture.md, docs/skills/plan.md and plugins/sdlc/skills/plan/state-format.md (plan Task 10) — verify: go test -tags integration ./internal/skillcheck/... and the plan Task 10 grep block <!-- ref:3-4-update-the-lanes-the-limits-and-the-a8cbaa -->

## Workflow follow-up

- Run `task deploy`, reload the plugin, then call `validate({action:"plan_gates", file:"<plan file>"})`. Expect findings or `findings: (none)`, with no error. A file with no `### Task N:` heading returns a DomainError.
- `/sdlc:ship` archives the change after validation passes.
