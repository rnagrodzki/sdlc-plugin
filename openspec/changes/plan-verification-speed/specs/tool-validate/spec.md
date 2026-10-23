# Spec Delta

## MODIFIED Requirements

### Requirement: Actions and input fields
The tool SHALL run the validator named by `action` and SHALL use only the input fields listed for that action; other fields are ignored.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `action` | string (enum) | yes | one of the 11 actions below, e.g. `plan_format` | Validator to run |
| `file` | string | `plan_format`, `plan_style` and `plan_gates` only | path, absolute or relative to the root of the action, e.g. `docs/plans/x.md` | Plan file to check |
| `final` | boolean | no | `true` / `false` | `plan_format`: also run PF9, and PF10 when `template` is set |
| `template` | string | no | path, e.g. `.sdlc-v2/plan-template.md` | `plan_format`: template for PF10 and for the PF13 measured sections; `plan_style`: template for the measured sections |
| `strict` | boolean | no | `true` / `false` | `cost_tiers`: report `INHERITED` as `error` instead of `warning` |
| `section` | string | no | config section name, e.g. `execute`; default `plan` | `guardrails`: section to read |
| `activeWorktree` | boolean | no | `true` / `false` | `guardrails`: read the active worktree's config |
| `candidatesJson` | string | no | JSON-encoded array of guardrail entries, e.g. `[{"id":"no-ci-bypass","description":"Plans must not skip CI.","severity":"error"}]` | `guardrails`: proposed entries checked in memory together with the section on disk; nothing is written |
| `body` | string | `pr_body` only | plain text PR body | PR body to check |

| `action` | Inputs used | Root read | Finding IDs |
|---|---|---|---|
| `plan_format` | `file`, `final`, `template` | main | `PF1`–`PF7`, `PF9`, `PF10`, `PF11`, `PF12`, `PF13`, `PF14` |
| `plan_style` | `file`, `template` | main | `PF13` |
| `plan_gates` | `file` | active | `G3`, `G4`, `G8`, `G9`, `G10`, `G12`, `G18` |
| `discovery` | none | main | `PD1`–`PD16` |
| `pr_template` | none | main | `V1`–`V5` |
| `cost_tiers` | `strict` | main | `INHERITED`, `MISSING_DOC`, `DRIFT`, `STALE_DOC`, `NO_COST_DOC` |
| `guardrails` | `section`, `activeWorktree`, `candidatesJson` | main; active with `activeWorktree: true` | the guardrail id |
| `dimensions` | none | active, else main | `D0`–`D13`, `UNKNOWN` |
| `pr_body` | `body` | main | `PR_BODY` |
| `ci_script_drift` | none | main | `CI_SCRIPT_OUTDATED`, `CI_SCRIPT_MISSING` |
| `worktree_anchoring` | none | main and active | `WORKTREE_ANCHOR_BARE`, `WORKTREE_ANCHOR_MISMATCH`, `WORKTREE_ANCHOR_STRAY_STATE` |

- Annotations: `Title: "Validate SDLC artifacts"`, `ReadOnly: true`, `Idempotent: true`, `OpenWorld: false`. No action writes files.
- The tool description names `candidatesJson` and the `fix` field for the `guardrails` action.

#### Scenario: Unused field is ignored
- **WHEN** the tool is called with `action: "discovery"` and `activeWorktree: true`
- **THEN** the findings are the same as without `activeWorktree`

### Requirement: Unknown action
The tool SHALL reject an `action` outside the enum with a `DomainError`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Unknown `action` | `DomainError` | `unknown validate action "<action>" (sdlc v<version>, commit <commit>)` / pass one of the valid actions, which the Suggestion lists by name, or update the sdlc plugin |

#### Scenario: Nonsense action
- **WHEN** the tool is called with `action: "nonsense"`
- **THEN** the result is a `DomainError` whose message starts with `unknown validate action "nonsense"`
- **AND** its Suggestion lists `plan_gates`

### Requirement: Root selection
The tool SHALL read from the main worktree root, except for `dimensions`, `ci_script_drift`, `plan_gates`, and opted-in `guardrails`, which read the active worktree root.

| Case | Root | Active root cannot be resolved |
|---|---|---|
| `dimensions` | active | falls back to main (no error) |
| `ci_script_drift` | active | falls back to main (no error) |
| `plan_gates` | active | `InfraError` `resolve active worktree for plan_gates: <cause>` |
| `guardrails` with `activeWorktree: true` | active | `InfraError` `resolve active worktree for guardrails activeWorktree:true: <cause>` |
| Every other case | main | n/a |
| Main root cannot be resolved (any action) | n/a | `InfraError` `resolve project root: <cause>` |

#### Scenario: Guardrails from a linked worktree
- **WHEN** the tool runs from a linked worktree whose `.sdlc-v2/config.toml` has guardrail `Bad_ID` and the main worktree's config does not
- **THEN** `action: "guardrails", activeWorktree: true` reports `Bad_ID`
- **AND** `action: "guardrails"` without the flag does not

#### Scenario: Opt-in with no active worktree fails loud
- **WHEN** the tool runs from the main repo's `.git` directory with `action: "guardrails", activeWorktree: true`
- **THEN** the result is an `InfraError` whose message contains `resolve active worktree`
- **AND** `action: "dimensions"` from the same place succeeds

#### Scenario: CI drift from a linked worktree
- **WHEN** CI files were scaffolded only in a linked worktree and the tool runs there with `action: "ci_script_drift"`
- **THEN** `findings` is an empty list

#### Scenario: plan_gates with no active worktree fails loud
- **WHEN** the tool runs from the main repo's `.git` directory with `action: "plan_gates"`
- **THEN** the result is an `InfraError` whose message starts with `resolve active worktree for plan_gates`

## ADDED Requirements

### Requirement: plan_gates action
The `plan_gates` action SHALL check a plan file against the gates G3, G4, G8, G9, G12 and the G18 floor. It SHALL return one finding for each offender, with the gate id as `id`, sorted by gate, then task number, then message. It requires `file` and writes nothing.

#### Scenario: Forward reference
- **WHEN** Task 2 depends on Task 3
- **THEN** the findings hold the `error` finding G3 `Task 2: depends on Task 3, which is not listed before it`

#### Scenario: Missing dependency target
- **WHEN** Task 2 depends on Task 9 and the plan has no Task 9
- **THEN** the findings hold the `error` finding G12 `Task 2: depends on nonexistent Task 9`

#### Scenario: Shared path with a dependency chain
- **WHEN** Tasks 1 and 3 modify one path, Task 3 depends on Task 2, and Task 2 depends on Task 1
- **THEN** the findings hold no G4 finding

#### Scenario: Shared path with no dependency
- **WHEN** Task 1 modifies `./a.go`, Task 2 modifies `a.go`, and no dependency links them
- **THEN** the findings hold one `error` finding G4 `Task 1 and Task 2 both modify a.go with no dependency path between them`

#### Scenario: Missing Verify field
- **WHEN** Task 1 has no `**Verify:**` line
- **THEN** the findings hold a `warning` finding G8 whose message is `Task 1` and the PF3 Verify text

#### Scenario: Too many files
- **WHEN** Task 1 touches 6 distinct paths
- **THEN** the findings hold the `warning` finding G9 `Task 1: touches 6 files (limit 5)`

#### Scenario: Too many Trivial tasks
- **WHEN** 5 of 5 tasks are Trivial
- **THEN** the findings hold the `warning` finding G9 `Plan: 5 of 5 tasks are Trivial (limit 80%)`

#### Scenario: Prose-only Contract
- **WHEN** the Contract block of the last task has prose only, and a `## Verification Scorecard` table follows it
- **THEN** the findings hold the `error` finding G18 `Task N: Contract has no code block, table or diagram` for that task

#### Scenario: No Contract line
- **WHEN** a task has no `**Contract:**` line
- **THEN** the findings hold no G18 finding for that task

#### Scenario: Not a plan
- **WHEN** the file has no `### Task` heading
- **THEN** the result is `findings: (none)`

#### Scenario: Empty file input
- **WHEN** `plan_gates` runs with an empty `file`
- **THEN** the result is a `DomainError` `plan_gates: file is required`

### Requirement: plan_gates Modify path check
The `plan_gates` action SHALL check each Modify path against the active worktree root and report each bad path as a G10 `error` finding. It SHALL return an `InfraError` when a path cannot be checked for a reason other than absence.

#### Scenario: Absent path
- **WHEN** Task 2 modifies `x.go`, `x.go` does not exist, and no earlier task creates it
- **THEN** the findings hold the G10 finding `Task 2: Modify path x.go does not exist`

#### Scenario: Path created by an earlier task
- **WHEN** Task 1 creates `x.go`, Task 2 modifies `x.go`, and `x.go` does not exist
- **THEN** the findings hold no G10 finding

#### Scenario: Folder path
- **WHEN** a Modify path of Task 2 is a folder
- **THEN** the findings hold a G10 finding that ends with `is not a regular file`

#### Scenario: Path outside the project
- **WHEN** a Modify path of Task 2 is `../x.go`
- **THEN** the findings hold a G10 finding that ends with `is outside the project`

#### Scenario: Parent is a file
- **WHEN** a Modify path is `a.txt/x.go` and `a.txt` is a regular file
- **THEN** the result is an `InfraError` whose message starts with `plan_gates: cannot check Modify path`
- **AND** its Suggestion is `Check that each parent of a.txt/x.go is a folder that you can read. If a parent is a file, correct the Modify path in the plan. Then retry plan_gates.`

#### Scenario: Path only in the active worktree
- **WHEN** a Modify path exists in the active worktree and not in the main worktree
- **THEN** the findings hold no G10 finding

### Requirement: Plan file read limit
The `plan_format`, `plan_style` and `plan_gates` actions SHALL refuse a plan file over 8 MiB (8388608 bytes) with a `DomainError` that names the action, the size and the limit, and SHALL read a file of exactly 8 MiB.

#### Scenario: File over the limit
- **WHEN** `plan_gates` runs on a file of 8388609 bytes
- **THEN** the result is a `DomainError` `plan_gates: file is 8388609 bytes, over the 8 MiB limit (8388608 bytes): <path>`

#### Scenario: File at the limit
- **WHEN** `plan_format` runs on a file of 8388608 bytes
- **THEN** the action reads the file and returns its findings
