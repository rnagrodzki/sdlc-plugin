# Spec Delta

## MODIFIED Requirements

### Requirement: Action dispatch
The tool SHALL run exactly one operation per call, selected by the `action` field, and SHALL ignore every input field that the selected action does not list, except `section`, `content` and `topicStatus`: a call with another action refuses them (see Requirement: Preplan write input errors).

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

- Every action returns `summary` and `next`.
- `material` and `triggers` are always present in the result, also for actions that do not set them (`material: false`, `triggers: (none)`).

#### Scenario: Unknown action
- **WHEN** `action` is not one of the twelve listed values
- **THEN** the tool returns a `DomainError` whose message starts with `unknown action "<value>"`
- **AND** the message lists `merge_results, material_snapshot, material_compare, openspec_appendix, openspec_instructions, openspec_stage, evidence_record, evidence_digest, evidence_get, preplan_context, preplan_check, preplan_write`

#### Scenario: Unused fields are ignored
- **WHEN** `action` is `merge_results` and the call also sets `filePath`
- **THEN** the tool merges the results and does not read `filePath`

## ADDED Requirements

### Requirement: Preplan section write
The `preplan_write` action SHALL replace the body under the first matching `## <section>` heading of an existing topic file with `content`, after `content` passes the topic-file format rules. It SHALL find and read the file as `preplan_check` does and SHALL never create one.

#### Scenario: Repeated write
- **WHEN** two calls send the same `Decisions` body
- **THEN** the file after the second call equals the file after the first call

#### Scenario: Absent section with empty content
- **WHEN** `section` is `Open questions`, `content` is empty and the file has no `## Open questions` heading
- **THEN** the file is unchanged and `written` is `(none)`

#### Scenario: Empty content deletes Open questions
- **WHEN** `section` is `Open questions`, `content` is empty and the file has a `## Open questions` section
- **THEN** the file has no `## Open questions` section

#### Scenario: Open questions added
- **WHEN** `section` is `Open questions`, `content` is `- Who owns the cut?` and the file has no `## Open questions` heading
- **THEN** the file has a `## Open questions` section after `## Decisions`

#### Scenario: File mode kept
- **WHEN** the topic file has mode 0600 and a write succeeds
- **THEN** the topic file still has mode 0600

### Requirement: Preplan format rules for every section
The `preplan_write` action SHALL refuse a body that breaks P1, P2 or P3 with a `DomainError` `Section "<section>" breaks rule <id>: <detail>`, and SHALL write nothing.

| Rule | Rule text |
|---|---|
| P1 | body is at most 65536 bytes |
| P2 | no line that starts with `# ` or `## ` outside a code fence |
| P3 | every code fence opened in the body closes in the body |

#### Scenario: Body too large
- **WHEN** the body of `content` is 65537 bytes after the line ends are normalised (CRLF to LF, final line ends removed)
- **THEN** the tool returns a `DomainError` that names rule `P1`, 65537 bytes and the limit 65536 bytes
- **AND** the file bytes are unchanged

#### Scenario: Heading in the body
- **WHEN** `content` for `Goal` holds the line `## Extra`
- **THEN** the tool returns a `DomainError` that names rule `P2`

### Requirement: Preplan format rules for each section
The `preplan_write` action SHALL refuse a body that breaks the rule of its section with a `DomainError` that names the rule, and SHALL write nothing.

| Rule | Section |
|---|---|
| P4-P7 | Decisions: table header, rows, notes, `Smallest cut:` line |
| P8 | Non-goals: bullets or `None stated.` |
| P9 | Open questions: bullets |
| P10 | Flows: `No flow change.` or a `mermaid` fence with allowed `classDef` lines |
| P11 | Guardrail check: empty body only |

#### Scenario: Repeated Decisions id
- **WHEN** the `Decisions` body has two rows with `#` value `2`
- **THEN** the tool returns a `DomainError` that names rule `P5`

#### Scenario: Smallest cut names a dropped row
- **WHEN** the `Decisions` body ends with `Smallest cut: #4.` and row 4 starts with `Dropped:`
- **THEN** the tool returns a `DomainError` that names rule `P7`

#### Scenario: Pastel classDef
- **WHEN** the `Flows` body holds `classDef new fill:#ccffcc`
- **THEN** the tool returns a `DomainError` that names rule `P10`

#### Scenario: Guardrail check body
- **WHEN** `section` is `Guardrail check` and `content` is not empty
- **THEN** the tool returns a `DomainError` that names rule `P11`

### Requirement: Preplan status write
The `preplan_write` action SHALL write `**Status:** <topicStatus>` in place of the first Status line in the first 4096 bytes. With no such line it SHALL insert one before the `**Started:**` line, else after the `# ` title line, else as the first line.

#### Scenario: No Status line
- **WHEN** the file has a `**Started:**` line and no Status line, and `topicStatus` is `paused`
- **THEN** the line `**Status:** paused` is before the `**Started:**` line

#### Scenario: Paused
- **WHEN** `topicStatus` is `paused`
- **THEN** `next` is `The topic file is paused. End the preplan run. Run /sdlc:preplan with the same topic to continue.`

#### Scenario: Section and status in one call
- **WHEN** a call sets `section` `Decisions` and `topicStatus` `ready for plan`
- **THEN** `written` holds `Decisions` and `Status`

### Requirement: Preplan ready list
When `topicStatus` is `ready for plan`, the `preplan_write` action SHALL return `readyTopicFiles` with this file first, then up to 3 other topic files with status `ready for plan` from the newest 100 topic files, newest first, as absolute paths, and `readyLeftOut` with the count of ready files not listed.

#### Scenario: Five ready files
- **WHEN** this file and 4 other topic files have status `ready for plan`
- **THEN** `readyTopicFiles` holds 4 paths, this file first
- **AND** `readyLeftOut` is 1

#### Scenario: Folder list fails
- **WHEN** the preplan folder list fails after the write
- **THEN** the call succeeds and `readyTopicFiles` holds this file only
- **AND** `summary` holds `Ready topic files not listed:`

### Requirement: Preplan write input errors
The tool SHALL return a `DomainError` and write nothing for each bad input below.

| Input | Message stem |
|---|---|
| `content` without `section` | `content needs section` |
| no `section` and no `topicStatus` | `nothing to write` |
| unknown `section` | `section "<s>" is not a topic-file section` |
| `topicStatus` not in the 3 values | `topicStatus "<v>" is not a preplan status` |
| `section`, `content` or `topicStatus` with another action | `<field> is a preplan_write field` |

#### Scenario: Field sent with another action
- **WHEN** `action` is `preplan_check` and the call sets `section`
- **THEN** the tool returns a `DomainError` `section is a preplan_write field`
- **AND** the check line is not written

#### Scenario: Section hidden by an open fence
- **WHEN** an open code fence comes before the `## Decisions` heading
- **THEN** the tool returns a `DataError` `section "Decisions" not found in <file>`
- **AND** its Suggestion names `preplan_context`
