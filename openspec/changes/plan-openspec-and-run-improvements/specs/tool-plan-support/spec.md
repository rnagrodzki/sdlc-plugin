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

- Every action returns `summary` and `next`.
- `material` and `triggers` are always present in the result, also for actions that do not set them (`material: false`, `triggers: (none)`).

#### Scenario: Unknown action
- **WHEN** `action` is not one of the nine listed values
- **THEN** the tool returns a `DomainError` whose message starts with `unknown action "<value>"`
- **AND** the message lists `merge_results, material_snapshot, material_compare, openspec_appendix, openspec_instructions, openspec_stage, evidence_record, evidence_digest, evidence_get`

#### Scenario: Unused fields are ignored
- **WHEN** `action` is `merge_results` and the call also sets `filePath`
- **THEN** the tool merges the results and does not read `filePath`

## ADDED Requirements

### Requirement: openspec_instructions
The `openspec_instructions` action SHALL copy `openspec/config.yaml` into a new temp directory, run
`openspec new change <changeName>`, `openspec status --change <changeName> --json`, and
`openspec instructions <artifact> --change <changeName> --json` for each artifact there, and SHALL
return `schemaName` and `artifacts[]` `{id, outputPath, requires, template, instruction, context, rules}`
in status order, plus `guardrails` — the same list `plan_prepare` returns. It writes nothing in the repository.

#### Scenario: New change
- **WHEN** `openspec_instructions` is called with `changeName:"add-widget"` and no such change exists
- **THEN** `artifacts` lists `proposal`, `specs`, `design`, `tasks` with their templates
- **AND** `git status --porcelain` prints nothing

### Requirement: openspec_stage
The `openspec_stage` action SHALL replace the staging dir for `changeName` with the given `files`, write `stage.json`, and validate a temp copy, as defined by the `openspec-staging` capability.

| Input | Encoding | Example |
|---|---|---|
| `changeName` | plain text, bare kebab-case | `add-widget` |
| `files` | JSON array of `{path, content}`; `path` relative to the change dir | `[{"path":"proposal.md","content":"# Proposal\n..."}]` |
| `planPath` | plain text, absolute path | `/Users/me/.claude/plans/add-widget.md` |

| Output | Meaning |
|---|---|
| `stagingDir` | Repo-relative staging dir, e.g. `.sdlc-v2/openspec-staging/add-widget/` |
| `files` | `[{path, sha256}]` as written to `stage.json` |
| `valid` | `true` when `openspec validate <changeName> --strict` exits 0 in the temp copy |
| `validateOutput` | CLI output of that validation |

- Each call replaces the whole staging dir, so a file dropped from `files` is removed.
- `next` is `Staged and valid. Add the **OpenSpec-Staging:** header to the plan.` when `valid`, else `Fix the artifacts using validateOutput and call openspec_stage again.`
- The tool keeps `ReadOnly: true`: it writes only gitignored paths and the OS temp dir, and no path comes from a caller field without the name and path checks.

| Condition | Class | Message (short) |
|---|---|---|
| `changeName` empty or not bare kebab-case | `DomainError` | `openspec_stage: invalid changeName "<name>"` |
| A `files[].path` fails the path check | `DomainError` | `openspec_stage: path "<path>" not allowed` |
| `openspec` CLI not on PATH | `InfraError` | `openspec CLI not found on PATH` |

#### Scenario: Stage and validate
- **WHEN** `openspec_stage` is called with `changeName:"add-widget"` and valid proposal, spec, design, and tasks files
- **THEN** `stagingDir` is `.sdlc-v2/openspec-staging/add-widget/`
- **AND** `valid` is `true`
- **AND** `git status --porcelain` prints nothing

#### Scenario: Restage drops a file
- **WHEN** a second call for `add-widget` omits `design.md`
- **THEN** `.sdlc-v2/openspec-staging/add-widget/design.md` no longer exists
