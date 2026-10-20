# Tasks

Dependencies of the task groups. The groups form one chain: each group builds a Go package that the group before it changes. Group 4 changes `internal/tools/validators.go`, which the tests of group 3 build, so it runs last.

```mermaid
flowchart LR
  G1["1. Render tool guard"] --> G2["2. Config write guard"] --> G3["3. Skill text and docs"] --> G4["4. Edit hook"]
```

## 1. Render tool guard

- [x] 1.1 Add `StampMirror`, `CheckMirror` and the three mirror states in `internal/dimensions/mirror.go` with table-driven tests in `internal/dimensions/mirror_test.go` (plan Task 1) — verify: `go test ./internal/dimensions/ -run 'TestStampMirror|TestCheckMirror'`
- [x] 1.2 Add the `overwrite` input, the marker line and the check of the current mirror in `internal/tools/dimensions_render.go`, with unit, error-site and integration tests (plan Task 2) — verify: `go test ./internal/tools/ -run 'TestDimensionsRender|TestToolErrorSites_DimensionsRenderMirrorCheck|TestMCPToolParameterDescriptions'` and `go test -tags integration ./internal/tools/ -run TestIntegration_DimensionsRender`

## 2. Config write guard

- [x] 2.1 Add the refusal of a whole-table guardrail key that removes an id in `internal/tools/setup_write.go`, change the two tests that pinned the removal, and add error-site and integration tests (plan Task 3) — verify: `go test ./internal/tools/ -run 'TestSetupWriteSections|TestDiskGuardrailIDsMatchConfigDecoder|TestToolErrorSites_SetupWriteGuardrailGuard|TestMCPToolParameterDescriptions'` and `go test -tags integration ./internal/tools/ -run TestIntegration_SetupWriteSections`

## 3. Skill text and docs

- [x] 3.1 Write one key for each id in Step 3 of `plugins/sdlc/skills/setup/setup-guardrails.md` and `plugins/sdlc/skills/setup/setup-execution-guardrails.md`, re-key two lines of `internal/skillcheck/skillcheck_worktree_test.go`, and add `internal/skillcheck/skillcheck_guardrail_write_test.go` (plan Task 4) — verify: `go test ./internal/skillcheck/...`
- [x] 3.2 Skip a refused mirror in step 5b of `plugins/sdlc/skills/harden/SKILL.md`, pass `overwrite: true` after confirmation in `plugins/sdlc/skills/setup/setup-dimensions.md`, update `docs/skills/harden.md`, and add `internal/skillcheck/skillcheck_mirror_guard_test.go` (plan Task 5) — verify: `go test ./internal/skillcheck/...`
- [x] 3.3 Remove the whole-table guardrail examples from six sites of `plugins/sdlc/skills/setup/SKILL.md`, update `docs/skills/setup.md`, and extend `internal/skillcheck/skillcheck_guardrail_write_test.go` (plan Task 6) — verify: `go test ./internal/skillcheck/...`

## 4. Edit hook

- [x] 4.1 Match `.md` dimension files, check the guardrails of `config.toml`, and add a note for a mirror with no valid marker line in `internal/hooks/post_tool_validate.go`. Export `ValidateGuardrailsAction` in `internal/tools/validators.go`. Update the hook tests and the hook row of `docs/plan-architecture.md` (plan Task 7) — verify: `go test ./internal/hooks/ -run 'TestPostToolValidate'` and `go test ./internal/tools/ -run 'TestValidateGuardrailsAction'`

## Workflow follow-up

- Run `task check`, then `task deploy` and reload the plugin. Call each changed tool once before the review. Edit one dimension file, `config.toml` and one mirror to see the hook.
- Archive the change after the project's review requirements are satisfied.
