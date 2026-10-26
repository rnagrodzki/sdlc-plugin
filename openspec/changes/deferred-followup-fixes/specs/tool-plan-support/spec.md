# Spec Delta

## MODIFIED Requirements

### Requirement: Evidence store location and run validation
The evidence actions SHALL store one run's evidence under `<main root>/.sdlc-v2/runs/<runId>.evidence/`, and SHALL accept only a `runId` that names an existing plan run state file `.sdlc-v2/runs/<runId>.json`.

```text
.sdlc-v2/runs/
├── <runId>.json              plan run state (written by plan_prepare / plan_mark)
└── <runId>.evidence/
    ├── <writerId>.json       one file per writer: writerId, status, updatedAt, items[]
    ├── brief.md              discovery brief (writerId main only)
    └── guardrails.md         guardrails snapshot (written by plan_prepare)
```

- A `runId` with a path separator or `..` is rejected before any file is read.
- Any `DomainError` from an evidence action leaves every file under `.sdlc-v2/runs/` unchanged.
- The evidence actions, `plan_mark` and `plan_prepare` return the same class and message for the same `runId` state.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `runId` empty | `DomainError` | `runId is required for <action>` / pass runId from plan_prepare's runId output |
| `runId` invalid or run state file absent | `DomainError` | `plan run <runId> not found` / `Pass the runId that plan_prepare returned in this session. After a compaction, use the runId of the "Resume with:" line. If that run no longer exists, call plan_prepare with no runId to start a new run.` |
| Run state file unreadable or not JSON | `InfraError` | `plan run <runId> state read failed` / fix or delete the state file, call plan_prepare again |
| OS read failure in the evidence directory | `InfraError` | `evidence read failed: <path>` / make sure `<runId>.evidence/` is a writable directory |
| OS write failure in the evidence directory | `InfraError` | `evidence write failed: <path>` / same as above |

#### Scenario: Path traversal runId
- **WHEN** an evidence action is called with `runId` `../x`
- **THEN** the tool returns `DomainError` `plan run ../x not found`
- **AND** no file under `.sdlc-v2/runs/` changes

#### Scenario: Evidence directory is a regular file
- **WHEN** `<runId>.evidence` exists as a regular file
- **THEN** each evidence action returns `InfraError` with a message starting `evidence read failed: `

#### Scenario: Not-found suggestion names this session's run
- **WHEN** `evidence_digest` is called with the `runId` of a deleted run
- **THEN** the tool returns `DomainError` `plan run <runId> not found`
- **AND** the suggestion starts with `Pass the runId that plan_prepare returned in this session.`

### Requirement: evidence_digest run summary
Without `statusOnly` the `evidence_digest` action SHALL also return a `digest` section that never contains an item `body`.

| Output | Meaning |
|---|---|
| `digest.runId` | The run ID |
| `digest.planFilePath` | Plan file path stored in the run state |
| `digest.userPrompt` | The user prompt stored in the run state's creation intent |
| `digest.guardrailsFile` | Path of `<runId>.evidence/guardrails.md`, or empty when it does not exist |
| `digest.briefPath` | Path of `<runId>.evidence/brief.md`, or empty when it does not exist |
| `digest.instructions` | `planStyle.instructions` from `.sdlc-v2/local.toml`, read fresh on every call |
| `digest.checkpoint` | The run checkpoint: `step`, `iteration`, `expectedWriters`, `updatedAt` |
| `digest.index` | Columns `id`, `writer`, `ref`, `summary`; writers in name order, items in file order; max 200 item rows plus one overflow row; `(no items recorded)` when empty |

- The overflow row is `| … | — | — | <n> more; use evidence_get writerIds |`.
- Table cells escape `|` as `\|`, replace line breaks with spaces, and show `—` for an empty value.
- Items of unreadable writer files are left out of the index.
- `summary` is `<runId> — step <step>, iteration <i>; writers: <d> done, <r> running, <m> missing, <s> stalled[, <u> unreadable]; <n> items; <c> custom instructions.` Step defaults to `0` without a checkpoint.
- `next` starts with `If the sdlc:plan skill instructions are not in context, invoke the sdlc:plan skill first` and names `continue at step <step> (iteration <i>)`.
- `next` says: when lanes and lensReviewers are not in context, call `plan_prepare({resume:true, resolveTemplate:true, skipConfigCheck:true, runId:"<runId>"})` first, with the `runId` of this call.
- When writers are missing or stalled, `next` ends with `Missing: <list>. Stalled: <list>. Wait one poll cycle (evidence_digest statusOnly); if a writer is still listed, re-dispatch it or force-progress past it.`

#### Scenario: Digest hides bodies
- **WHEN** a writer stored an item with body `BODY-SECRET-ONE` and `evidence_digest` is called
- **THEN** the rendered output does not contain `BODY-SECRET-ONE`
- **AND** `digest.index` contains `| F-auth-1 | explore-auth-flow | internal/auth.go:42 | token check skips expiry |`

#### Scenario: Resume point from checkpoint
- **WHEN** the run checkpoint has step `6.5` and iteration 2
- **THEN** `next` contains `continue at step 6.5 (iteration 2)`

#### Scenario: Resume call names the run
- **WHEN** `evidence_digest` is called with `runId` `plan-main-20261026T084445Z`
- **THEN** `next` contains `plan_prepare({resume:true, resolveTemplate:true, skipConfigCheck:true, runId:"plan-main-20261026T084445Z"})`

#### Scenario: Index cap
- **WHEN** the run holds 201 items
- **THEN** `digest.index` has 200 item rows
- **AND** its last row is `| … | — | — | 1 more; use evidence_get writerIds |`

#### Scenario: Custom instructions added
- **WHEN** `.sdlc-v2/local.toml` gains `[planStyle] instructions = ["Cite file:line.", "Ask before adding a dependency."]` between two calls
- **THEN** the second call's `digest.instructions` holds both entries
- **AND** `summary` contains `; 2 custom instructions.`
