# Spec Delta

## MODIFIED Requirements

### Requirement: Input fields
The tool SHALL accept the input fields below. All fields are optional; a zero value means "not set".

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `skipConfigCheck` | boolean | no | plain | Skip the config-version check |
| `fromOpenspec` | string | no | plain text | OpenSpec change name to validate and load, e.g. `add-widget` |
| `resolveTemplate` | boolean | no | plain | Resolve the active plan template and return `template` |
| `fromOpenspecDirect` | boolean | no | plain | Plan is built directly from an OpenSpec change |
| `openspecStage` | boolean | no | plain | Plan authors a new OpenSpec change and stages it, e.g. `true` |
| `lightweight` | boolean | no | plain | Request lightweight routing |
| `fileCount` | integer | no | plain | Expected number of files the change touches |
| `userPrompt` | string | no | plain text | The user's plan request; feeds discovery and is saved in the run |
| `resume` | boolean | no | plain | Post-compact recovery: reuse the active run, e.g. `true` |

- `openspecInlineGenerate` is removed from the input schema and is never read.

#### Scenario: Minimal call
- **WHEN** `plan_prepare({skipConfigCheck: true})` is called in a git repository with no OpenSpec, no config, and no plan template
- **THEN** `fromOpenspec` is null, `guardrails` is empty, `planTemplate.path` is null, `openspec.present` is `false`
- **AND** `errors` is empty

#### Scenario: Removed field
- **WHEN** the tool's input schema is listed
- **THEN** it has `openspecStage` and has no `openspecInlineGenerate`

### Requirement: OpenSpec detection
The tool SHALL report OpenSpec state of the active worktree in `openspec`, taking the change list from `openspec list --json` and per-change artifacts from `openspec status --change <name> --json`, never from its own directory globs.

- `present` is `true` only when `openspec/config.yaml` exists; then `authoritative` is `{path: "openspec/config.yaml", specsCount}`, with `specsCount` from `openspec list --specs --json`.
- `activeChanges[]` lists each change the CLI reports that has `proposal.md`, with `name`, `stage`, `deltaSpecCount`, `hasProposal`, `hasDesign`, `hasTasks`, `tasksDone`, `tasksTotal`.
- `deltaSpecCount` counts every delta spec file the CLI lists for the change, at any depth under `specs/`.
- `stage` is `spec-in-progress` (no tasks), `ready-for-plan` (0 done), `implementation-in-progress`, or `tasks-complete`.
- `groupedChanges[]` lists each CLI warning with code `nested_change_directory` as `{name, nested[], message}`; grouped entries never appear in `activeChanges[]`.
- `branchMatch` is the change name that matches the branch name at a `/` or `-` boundary (case-insensitive, after removing a `feat/`, `fix/`, `chore/`, `refactor/` or `docs/` prefix). Otherwise, on a branch other than the default branch, it is the single change whose files the branch diff touches. Else it is null.
- When the `openspec` CLI is missing or fails, `present` still reflects `openspec/config.yaml`, `activeChanges` is empty, and `errors` gets `openspec CLI unavailable: <cause>`.

#### Scenario: One active change
- **WHEN** `openspec/config.yaml` exists and change `add-widget` has `proposal.md` and a `tasks.md` with 1 of 2 tasks done
- **THEN** `openspec.activeChanges[0].name` is `add-widget` and its `stage` is `implementation-in-progress`
- **AND** `openspec.authoritative.path` is `openspec/config.yaml`

#### Scenario: Nested delta specs counted
- **WHEN** change `add-widget` has `specs/a/spec.md` and `specs/identity/b/spec.md`
- **THEN** its `deltaSpecCount` is `2`

#### Scenario: Grouped change
- **WHEN** `openspec/changes/grp/demo/` exists
- **THEN** `openspec.groupedChanges[0]` is `{name:"grp", nested:["grp/demo"], message:<CLI message>}`
- **AND** no `activeChanges[]` entry is named `grp`

### Requirement: From-OpenSpec validation
When `fromOpenspec` is set, the tool SHALL validate the change through `openspec status --change <fromOpenspec> --json` and return `fromOpenspec` with `valid`, `changeName`, `hasProposal`, `deltaSpecCount`, `deltaSpecPaths`, `hasDesign`, `hasTasks`, `tasksDone`, `tasksTotal`, `stage`.

| Condition | `valid` | Added to `errors` |
|---|---|---|
| Name contains a path separator | `false` | `Invalid change name '<name>': grouped changes are not supported — rename to a flat name` |
| Change directory missing | `false` | `Change directory not found: openspec/changes/<name>/` |
| `proposal.md` missing | `false` | `Missing required file: openspec/changes/<name>/proposal.md` |
| No delta spec files | unchanged | nothing (warning only) |

- `deltaSpecPaths` lists every delta spec file at any depth, repo-relative.

#### Scenario: Unknown change
- **WHEN** `plan_prepare({fromOpenspec: "does-not-exist"})` is called
- **THEN** `fromOpenspec.valid` is `false`
- **AND** `errors` is not empty

#### Scenario: Grouped name
- **WHEN** `plan_prepare({fromOpenspec: "grp/demo"})` is called
- **THEN** `fromOpenspec.valid` is `false`
- **AND** `errors` contains `Invalid change name 'grp/demo': grouped changes are not supported — rename to a flat name`

### Requirement: Skeleton section bodies
The tool SHALL fill each skeleton section body by the first matching rule below. OpenSpec is active when `fromOpenspecDirect` or `openspecStage` is true.

| Rule | Body |
|---|---|
| Condition starts with `source matches openspec/changes/` and OpenSpec is not active | `Not applicable — no OpenSpec change` |
| Condition that does not start with `source matches openspec/changes/` | `Not applicable — condition "<condition>" not recognized` |
| Section `Deviations & assumptions` | Table `\| Item \| asked \| does \| why \|` with one `[TBD]` row |
| Section `Verification Scorecard` and (`lightweight` or `pipelineMode` is not `full`) | `Not applicable — lightweight plan` |
| Otherwise | `[TBD]` |

#### Scenario: OpenSpec section with a direct change
- **WHEN** the template has an OpenSpec-conditional section and `fromOpenspecDirect` is `true`
- **THEN** that section's body is `[TBD]`

#### Scenario: OpenSpec section with a staged change
- **WHEN** the template has an OpenSpec-conditional section and `openspecStage` is `true`
- **THEN** that section's body is `[TBD]`

#### Scenario: Lightweight plan
- **WHEN** `lightweight` is `true`
- **THEN** the `Verification Scorecard` body is `Not applicable — lightweight plan`
