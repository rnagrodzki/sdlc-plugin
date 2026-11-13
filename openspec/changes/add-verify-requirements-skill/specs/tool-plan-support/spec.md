# Spec Delta

## MODIFIED Requirements

### Requirement: Action dispatch
The tool SHALL run exactly one operation per call, selected by the `action` field, and SHALL ignore every input field that the selected action does not list, except `section`, `content`, `topicStatus`, `verifyKind` and `verifyValue`: a call with another action refuses them (see Requirement: Preplan write input errors and Requirement: verify_requirements_context inputs).

| Action | Required fields | Optional fields | Result fields | Writes |
|---|---|---|---|---|
| `merge_results` | `laneResults` or `lensResults` (at least one) | `expectedGates`, `isRedispatch` | `allIssues`, `coverageGaps`, `laneFailures`, `mergedStatus`, `recommendations` | nothing |
| `material_snapshot` | `filePath` | — | `snapshotPath` | one temp snapshot file |
| `material_compare` | `filePath`, `snapshotPath` | — | `material`, `triggers` | nothing |
| `openspec_appendix` | `changeName` | `proposalPath`, `designPath`, `specPaths`, `planTasks` | `appendixMarkdown` | nothing |
| `openspec_instructions` | `changeName` | — | `schemaName`, `artifacts`, `guardrails` | one temp dir |
| `openspec_stage` | `changeName`, `files` | `planPath` | `stagingDir`, `files`, `valid`, `validateOutput` | `<main-worktree>/.sdlc-v2/openspec-staging/<changeName>/`, one temp validation dir |
| `evidence_record` | `runId`, `writerId` | `status`, `items`, `brief` | `record` | `<writerId>.json`, `brief.md` |
| `evidence_digest` | `runId` | `expectedWriters`, `timeoutSeconds`, `statusOnly` | `writers`, `digest` (not with `statusOnly`) | nothing |
| `evidence_get` | `runId`, plus `ids` or `writerIds` | — | `get` | nothing |
| `preplan_context` | `topic` | — | `guardrails`, `preplanFile`, `preplanCreated` | `<main-worktree>/.sdlc-v2/preplan/YYYY-MM-DD-<slug>.md`, only when no topic file of the slug exists |
| `preplan_check` | `topic`, `passCount`, `keptCount`, `droppedCount` | — | `preplanFile`, `guardrailCheckLine` | the existing topic file of the slug: a temp file in the same folder, then a rename |
| `preplan_write` | `topic`, plus `section` or `topicStatus` | `content` (with `section` only) | `preplanFile`, `written`, `readyTopicFiles`, `readyLeftOut` | the existing topic file of the slug: a temp file in the same folder, then a rename |
| `verify_requirements_context` | `verifyKind`, `verifyValue` | — | `verifyKind`, `verifySource`, `structureChecks`, `structureFindings`, `openQuestions`, `guardrails`, `guardrailsStatus`, `jiraProject`, `issueTitle`, `issueBody`, `openspecStatus`, `openspecMainSpecs`, `openspecActiveChanges`, `openspecWarnings` | nothing |

- Every action returns `summary` and `next`.
- `material` and `triggers` are always present in the result, also for actions that do not set them (`material: false`, `triggers: (none)`).

#### Scenario: Unknown action
- **WHEN** `action` is not one of the thirteen listed values
- **THEN** the tool returns a `DomainError` whose message starts with `unknown action "<value>"`
- **AND** the message lists `merge_results, material_snapshot, material_compare, openspec_appendix, openspec_instructions, openspec_stage, evidence_record, evidence_digest, evidence_get, preplan_context, preplan_check, preplan_write, verify_requirements_context`

#### Scenario: Unused fields are ignored
- **WHEN** `action` is `merge_results` and the call also sets `filePath`
- **THEN** the tool merges the results and does not read `filePath`

### Requirement: Registration and annotations
The tool SHALL be registered as `plan_support`, described as `INTERNAL — called by sdlc skills only`, with the annotations below.

| Annotation | Value |
|---|---|
| `Title` | `Plan support and evidence store` |
| `ReadOnly` | `true` |
| `Idempotent` | `true` |
| `OpenWorld` | `true` |

#### Scenario: Client lists tools
- **WHEN** an MCP client lists the server's tools
- **THEN** `plan_support` reports `Title` `Plan support and evidence store`, `ReadOnly` `true`, `Idempotent` `true`, `OpenWorld` `true`

## ADDED Requirements

### Requirement: verify_requirements_context inputs
The action SHALL require `verifyKind` (one of `topic`, `file`, `jira`, `issue`) and `verifyValue` (at most 4096 bytes), and SHALL refuse `topic`, `section`, `content` and `topicStatus`.

#### Scenario: Bad kind
- **WHEN** `verifyKind` is omitted or not in the enum
- **THEN** the tool returns a `DomainError` that starts with `verify_requirements_context: verifyKind must be one of topic, file, jira, issue`

#### Scenario: Value too long
- **WHEN** `verifyValue` is 4097 bytes
- **THEN** the tool returns a `DomainError` that starts with `verify_requirements_context: verifyValue is 4097 bytes; the limit is 4096 bytes`

#### Scenario: Field of another action
- **WHEN** `action` is `preplan_check` and the call sets `verifyKind`
- **THEN** the tool returns a `DomainError` that starts with `preplan_check: verifyKind is a verify_requirements_context field`

### Requirement: verify_requirements_context writes nothing
The action SHALL write, create and delete no file for every input kind.

#### Scenario: Topic with no file
- **WHEN** `verifyKind` is `topic` and no topic file of the slug exists
- **THEN** the tool returns a `DomainError` with the Suggestion `Check the topic name against the files in .sdlc-v2/preplan/. For a file outside that folder, run /sdlc:verify-requirements --file <path>.`
- **AND** no file is created

### Requirement: verify_requirements_context topic kind
The action SHALL find the topic file as `preplan_check` does and SHALL return `structureFindings` and `openQuestions` with `structureChecks` `ran`.

#### Scenario: Cut line names a dropped row
- **WHEN** row `#2` starts with `Dropped:` and the cut line is `Smallest cut: #1, #2.`
- **THEN** `structureFindings` holds `{rule: cut-not-live, section: "## Decisions", row: "#2"}`

#### Scenario: Two matching topic files
- **WHEN** two topic files match the slug
- **THEN** the tool returns a `DataError`

### Requirement: verify_requirements_context file kind
The action SHALL accept a regular file of at most 8 MiB, with a relative path resolved against the active worktree, and SHALL return `structureChecks` `not run: the input is not a topic file`.

#### Scenario: No file at the path
- **WHEN** no file exists at `verifyValue`
- **THEN** the tool returns a `DomainError` that starts with `verify_requirements_context: no file at`

#### Scenario: File over 8 MiB
- **WHEN** the file is 8388609 bytes
- **THEN** the tool returns a `DataError` that names the size and the limit `8388608 bytes (8 MiB)`

### Requirement: verify_requirements_context jira kind
The action SHALL check the key format and SHALL return `jiraProject` with `structureChecks` `not run: the input is a Jira ticket`. It SHALL NOT read Jira.

#### Scenario: Valid key
- **WHEN** `verifyValue` is `INT-4571`
- **THEN** the result holds `verifySource: INT-4571` and `jiraProject: INT`

#### Scenario: Bad key
- **WHEN** `verifyValue` is `int4571`
- **THEN** the tool returns a `DomainError` that starts with `verify_requirements_context: int4571 is not a Jira key`

### Requirement: verify_requirements_context issue kind
The action SHALL accept an issue number of 1 to 10 digits with no leading zero, SHALL run `gh issue view <n> --json title,body` in the active worktree, and SHALL return `issueTitle` and `issueBody` as data.

#### Scenario: Issue read
- **WHEN** `gh` prints `{"title":"T","body":"B"}` for issue 42
- **THEN** the result holds `issueTitle: T`, `issueBody: B`, `verifySource: #42`

#### Scenario: Issue not found
- **WHEN** `gh` fails with `Could not resolve to an Issue`
- **THEN** the tool returns a `DomainError` that starts with `verify_requirements_context: issue #<n> not found in this repository`

#### Scenario: gh not authenticated
- **WHEN** `gh` fails with `gh auth login` in its error text
- **THEN** the tool returns an `InfraError` with the Suggestion `Run gh auth login, then run /sdlc:verify-requirements again.`

### Requirement: verify_requirements_context OpenSpec lists
The action SHALL return the main specs and the active changes of the active worktree, each spec with its requirement headings, and SHALL report a read failure as a warning line, not as an error.

#### Scenario: No OpenSpec
- **WHEN** `openspec/config.yaml` does not exist
- **THEN** `openspecStatus` reads `not present: openspec/config.yaml not found` and both lists render `(none)`

#### Scenario: Archived change
- **WHEN** a change is under `openspec/changes/archive/`
- **THEN** `openspecActiveChanges` does not hold the change

#### Scenario: Spec over 1 MiB
- **WHEN** a spec file is larger than 1 MiB
- **THEN** the list holds the spec with `requirements: []` and `openspecWarnings` names it
