# Spec Delta

## MODIFIED Requirements

### Requirement: Action dispatch
The tool SHALL run exactly one operation per call, selected by the `action` field, and SHALL ignore every input field that the selected action does not list.

| Action | Required fields | Optional fields | Result fields | Writes |
|---|---|---|---|---|
| `merge_results` | `laneResults` or `lensResults` (at least one) | `expectedGates`, `isRedispatch` | `allIssues`, `coverageGaps`, `laneFailures`, `mergedStatus`, `recommendations` | nothing |
| `material_snapshot` | `filePath` | — | `snapshotPath` | one temp snapshot file |
| `material_compare` | `filePath`, `snapshotPath` | — | `material`, `triggers` | nothing |
| `openspec_appendix` | `changeName` | `proposalPath`, `designPath`, `specPaths`, `planTasks` | `appendixMarkdown` | nothing |
| `openspec_instructions` | `changeName` | — | `schemaName`, `artifacts`, `guardrails` | one temp dir |
| `openspec_stage` | `changeName`, `files` | `planPath` | `stagingDir`, `files`, `valid`, `validateOutput` | `<active-worktree>/.sdlc-v2/openspec-staging/<changeName>/`, one temp validation dir |
| `evidence_record` | `runId`, `writerId` | `status`, `items`, `brief` | `record` | `<writerId>.json`, `brief.md` |
| `evidence_digest` | `runId` | `expectedWriters`, `timeoutSeconds`, `statusOnly` | `writers`, `digest` (not with `statusOnly`) | nothing |
| `evidence_get` | `runId`, plus `ids` or `writerIds` | — | `get` | nothing |
| `preplan_context` | `topic` | — | `guardrails`, `preplanFile`, `preplanCreated` | `<main-worktree>/.sdlc-v2/preplan/<slug>.md`, only when absent |

- Every action returns `summary` and `next`.
- `material` and `triggers` are always present in the result, also for actions that do not set them (`material: false`, `triggers: (none)`).

#### Scenario: Unknown action
- **WHEN** `action` is not one of the ten listed values
- **THEN** the tool returns a `DomainError` whose message starts with `unknown action "<value>"`
- **AND** the message lists `merge_results, material_snapshot, material_compare, openspec_appendix, openspec_instructions, openspec_stage, evidence_record, evidence_digest, evidence_get, preplan_context`

#### Scenario: Unused fields are ignored
- **WHEN** `action` is `merge_results` and the call also sets `filePath`
- **THEN** the tool merges the results and does not read `filePath`

## ADDED Requirements

### Requirement: Preplan context
The `preplan_context` action SHALL return the plan guardrails and the topic file path, SHALL create the topic file with a fixed skeleton only when it is absent, and SHALL NOT start a plan run.

- The file name is the slug of the topic: lowercase ASCII letters, digits and `-`.
- A topic is 1-50 characters on one line, with at least one ASCII letter or digit.
- Skeleton sections, in order: `## Goal`, `## Users and effect`, `## Flows`, `## Decisions`, `## Open questions`, `## Guardrail check`.

#### Scenario: New topic
- **WHEN** `topic` is `auth flow` and `<main-worktree>/.sdlc-v2/preplan/auth-flow.md` does not exist
- **THEN** the file exists with the skeleton text
- **AND** `## Flows` comes right after `## Users and effect`
- **AND** `preplanCreated` is `true`

#### Scenario: Existing topic
- **WHEN** the topic file already exists
- **THEN** the file content does not change
- **AND** `preplanCreated` is `false`

#### Scenario: Bad topic
- **WHEN** `topic` is `!!!`
- **THEN** the tool returns a `DomainError` with a Suggestion
- **AND** no file is written

#### Scenario: No guardrails configured
- **WHEN** the config has no plan guardrails
- **THEN** the summary says `0 guardrail(s) loaded — none configured.`
- **AND** `next` is not empty

### Requirement: Stage error for a target spec
The `openspec_stage` action SHALL return an InfraError with a Suggestion when it cannot copy a current target spec into its validation copy.

#### Scenario: Target spec too large
- **WHEN** `openspec/specs/tool-validate/spec.md` is over 1 MiB and the change has a delta for `tool-validate`
- **THEN** the tool returns an InfraError that names the file
- **AND** the Suggestion is `Check read permission on the named spec under openspec/specs/, then call openspec_stage again.`
