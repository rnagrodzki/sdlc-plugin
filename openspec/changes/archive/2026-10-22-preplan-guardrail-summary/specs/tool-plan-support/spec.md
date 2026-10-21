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
| `preplan_context` | `topic` | — | `guardrails`, `preplanFile`, `preplanCreated` | `<main-worktree>/.sdlc-v2/preplan/YYYY-MM-DD-<slug>.md`, only when no topic file of the slug exists |
| `preplan_check` | `topic`, `passCount`, `keptCount`, `droppedCount` | — | `preplanFile`, `guardrailCheckLine` | the existing topic file of the slug: a temp file in the same folder, then a rename |

- Every action returns `summary` and `next`.
- `material` and `triggers` are always present in the result, also for actions that do not set them (`material: false`, `triggers: (none)`).

#### Scenario: Unknown action
- **WHEN** `action` is not one of the eleven listed values
- **THEN** the tool returns a `DomainError` whose message starts with `unknown action "<value>"`
- **AND** the message lists `merge_results, material_snapshot, material_compare, openspec_appendix, openspec_instructions, openspec_stage, evidence_record, evidence_digest, evidence_get, preplan_context, preplan_check`

#### Scenario: Unused fields are ignored
- **WHEN** `action` is `merge_results` and the call also sets `filePath`
- **THEN** the tool merges the results and does not read `filePath`

### Requirement: Preplan context
The `preplan_context` action SHALL return the plan guardrails and the topic file path, SHALL create the topic file with a fixed skeleton only when no topic file of the slug exists, and SHALL NOT start a plan run.

- The slug of the topic: lowercase ASCII letters, digits and `-`.
- A topic is 1-50 characters on one line, with at least one ASCII letter or digit.
- The topic file of a slug is the one file in `.sdlc-v2/preplan/` named exactly `<slug>.md` or `YYYY-MM-DD-<slug>.md` (4, 2 and 2 digits). A new file is `YYYY-MM-DD-<slug>.md`, with the UTC date of the call.
- The tool reads at most 10000 names of the folder.
- Skeleton: the `# Preplan: <topic>` heading, the `**Status:** in progress` line, the `**Started:** <UTC RFC 3339 time>` line, then the sections `## Goal`, `## Users and effect`, `## Flows`, `## Decisions`. The skeleton has no `## Open questions` section and no `## Guardrail check` section.
- A symlink whose target does not exist, at `.sdlc-v2/preplan` or at the topic file, returns an `InfraError` that names the link and the target. Nothing is written.

#### Scenario: New topic
- **WHEN** `topic` is `auth flow`, the UTC date is `2026-10-12`, and `<main-worktree>/.sdlc-v2/preplan/` holds no topic file of the slug `auth-flow`
- **THEN** `<main-worktree>/.sdlc-v2/preplan/2026-10-12-auth-flow.md` exists with the skeleton text
- **AND** the file holds a `**Started:**` line with the UTC time of the call, after the `**Status:**` line
- **AND** `## Flows` comes right after `## Users and effect`
- **AND** the file ends after the `## Decisions` table header
- **AND** `preplanCreated` is `true`
- **AND** the summary says `Created topic file 2026-10-12-auth-flow.md.`

#### Scenario: Existing topic
- **WHEN** the folder holds `auth-flow.md` or `2026-10-01-auth-flow.md`
- **THEN** the tool returns that file
- **AND** the file content does not change
- **AND** `preplanCreated` is `false`
- **AND** `next` is `Read preplanFile, then continue with its open questions, if any.`

#### Scenario: Name that does not match
- **WHEN** the folder holds only `other-auth-flow.md`, `2026-1-12-auth-flow.md` and `auth-flow.md.bak`
- **THEN** the tool creates a new dated topic file

#### Scenario: Two matching files
- **WHEN** the folder holds `auth-flow.md` and `2026-10-01-auth-flow.md`
- **THEN** the tool returns a `DataError` that names both files
- **AND** the Suggestion is `Rename or delete one of: 2026-10-01-auth-flow.md, auth-flow.md. Then run /sdlc:preplan again.`
- **AND** no file is written

#### Scenario: Folder that cannot be listed
- **WHEN** a regular file is at the `.sdlc-v2/preplan` path
- **THEN** the tool returns an `InfraError` whose message starts with `preplan_context: list: `
- **AND** the Suggestion starts with `Make <dir> a folder that you can read`
- **AND** no file is written

#### Scenario: Folder over the bound
- **WHEN** `.sdlc-v2/preplan` holds more than 10000 names
- **THEN** the tool returns an `InfraError` with the Suggestion `Move old topic files out of <dir>. Then run /sdlc:preplan again.`
- **AND** no file is written

#### Scenario: Missing folder
- **WHEN** `.sdlc-v2/preplan` does not exist
- **THEN** the tool creates the folder and a dated topic file

#### Scenario: Bad topic
- **WHEN** `topic` is `!!!`
- **THEN** the tool returns a `DomainError` with a Suggestion
- **AND** no file is written

#### Scenario: No guardrails configured
- **WHEN** the config has no plan guardrails
- **THEN** the summary says `0 guardrail(s) loaded — none configured.`
- **AND** `next` is not empty

#### Scenario: Guardrails unavailable
- **WHEN** the plan config cannot be read
- **THEN** `next` ends with `preplan_check writes the warning into the Guardrail check line at Step 4.`

#### Scenario: Dangling preplan folder link
- **WHEN** `.sdlc-v2/preplan` is a symlink to `<target>`, which does not exist
- **THEN** the tool returns an `InfraError` whose message names `.sdlc-v2/preplan` and `<target>`
- **AND** the Suggestion is `Start a new session so the session-start hook repairs the link, or run: mkdir -p <target>`
- **AND** `<target>` is not created

#### Scenario: Dangling topic file link
- **WHEN** the topic file path is a symlink whose target does not exist
- **THEN** the tool returns an `InfraError` with the Suggestion `Remove the link, then try again: rm <link>`
- **AND** the result is not `preplanCreated: false`

## ADDED Requirements

### Requirement: Preplan check line
The `preplan_check` action SHALL write one `**Guardrail check:**` line into the existing topic file of the topic, and SHALL NOT create a topic file.

- Line form: `**Guardrail check:** <G> guardrails, <P> proposals: <a> pass, <b> kept, <c> dropped. Last check: <UTC RFC 3339 time>.`
- `<G>` is the loaded guardrail count. `<P>` is the sum of the three counts.

#### Scenario: First check
- **WHEN** the topic file `2026-10-21-auth-flow.md` has no check line, 53 guardrails load, the counts are 6, 1 and 0, and the UTC time is `2026-10-21T17:05:00Z`
- **THEN** the line after the `**Started:**` line and one blank line is `**Guardrail check:** 53 guardrails, 7 proposals: 6 pass, 1 kept, 0 dropped. Last check: 2026-10-21T17:05:00Z.`
- **AND** every other byte of the file is the same
- **AND** the summary is `Wrote the Guardrail check line of 2026-10-21-auth-flow.md.`
- **AND** `next` is `Read preplanFile again before your next edit: preplan_check rewrote it. Then follow the Step 4 route table.`

#### Scenario: No Started line
- **WHEN** the first 4096 bytes have a `**Status:**` line, no `**Started:**` line and no check line
- **THEN** the tool adds the check line after the `**Status:**` line

#### Scenario: Later check
- **WHEN** the first 4096 bytes of the topic file hold a check line and the counts are 2, 0 and 0
- **THEN** the tool replaces that line
- **AND** the file has exactly one check line
- **AND** the line shows `2 proposals: 2 pass, 0 kept, 0 dropped`

#### Scenario: Undated file name
- **WHEN** the only topic file of the slug `auth-flow` is `auth-flow.md`
- **THEN** the tool writes the check line into `auth-flow.md`

#### Scenario: Guardrails unavailable
- **WHEN** the plan config cannot be read
- **THEN** the check line ends with ` Guardrails unavailable: Failed to read plan config: <error text>.` on the same line

#### Scenario: Clock not in UTC
- **WHEN** the clock of the server is in UTC+2
- **THEN** the time in the check line ends with `Z`

#### Scenario: Line end of the file
- **WHEN** the topic file uses CR LF line ends
- **THEN** the new check line ends with CR LF

### Requirement: Preplan check errors
The `preplan_check` action SHALL return an error with a Suggestion and SHALL leave the topic file unchanged when the input or the topic file is not valid.

#### Scenario: Missing count
- **WHEN** `keptCount` is absent
- **THEN** the tool returns a `DomainError` whose message is `preplan_check: keptCount is required`
- **AND** the file bytes do not change

#### Scenario: Negative count
- **WHEN** `passCount` is `-1`
- **THEN** the tool returns a `DomainError` whose message is `preplan_check: passCount is -1, must be 0 or more`

#### Scenario: Bad topic
- **WHEN** `topic` is `!!!`
- **THEN** the tool returns a `DomainError` whose message starts with `preplan_check:`

#### Scenario: No topic file
- **WHEN** `topic` is `no such topic` and no topic file of its slug exists
- **THEN** the tool returns a `DomainError` whose message contains `no topic file for topic`
- **AND** the Suggestion is `Run plan_support with action preplan_context and this topic first.`
- **AND** no file is created

#### Scenario: Two topic files
- **WHEN** the folder holds `auth-flow.md` and `2026-10-01-auth-flow.md`
- **THEN** the tool returns a `DataError` that names both files
- **AND** the Suggestion is `Rename or delete one of: 2026-10-01-auth-flow.md, auth-flow.md. Then run /sdlc:preplan again.`

#### Scenario: Symlink at the topic file
- **WHEN** the topic file path is a symlink, a folder or a named pipe
- **THEN** the tool returns a `DomainError`
- **AND** the link and its target do not change

#### Scenario: File over the read limit
- **WHEN** the topic file has 8 MiB plus 1 byte
- **THEN** the tool returns a `DataError`
- **AND** the Suggestion is `Make the topic file smaller than 8 MiB by hand, then call preplan_check again.`

#### Scenario: No header line
- **WHEN** the first 4096 bytes hold no check line, no `**Started:**` line and no `**Status:**` line
- **THEN** the tool returns a `DataError`
- **AND** the Suggestion is `Add a **Status:** line near the top of the topic file by hand, then call preplan_check again.`

#### Scenario: Write failure
- **WHEN** the rename of the temp file fails
- **THEN** the tool returns an `InfraError`
- **AND** the topic file stays as it was
