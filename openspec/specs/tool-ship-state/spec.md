# tool-ship-state Specification

## Purpose
MCP tool `ship_state` reads and updates the ship pipeline's run state: step lifecycle, decisions, deferred findings, self-healing records, resume data, cleanup, garbage collection, the end-of-run report, and the durable history store. The `ship` skill and its sub-skills (`received-review`, `harden`, `deferred`) call it throughout a run. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Action dispatch
The tool SHALL select one operation from the `action` input and SHALL return a `DomainError` that lists every accepted action when `action` is unknown.

| Action | Group | Writes |
|---|---|---|
| `begin-step` | step lifecycle | ship state file |
| `complete-step` | step lifecycle | ship state file, `.sdlc-v2/timings.json` |
| `skip` | step lifecycle | ship state file |
| `fail` | step lifecycle | ship state file |
| `start` | step lifecycle (legacy alias of `begin-step`) | ship state file |
| `complete` | step lifecycle (legacy alias of `complete-step`) | ship state file, `.sdlc-v2/timings.json` |
| `decide` | run records | ship state file |
| `defer` | run records | ship state file, `.sdlc-v2/history/deferred.json` |
| `healing_record` | run records | ship state file |
| `harden_clusters` | query | nothing |
| `read` | query | nothing |
| `next` | query | nothing |
| `todos` | query | nothing |
| `report` | end of run | `.sdlc-v2/reports/` when `detail.write` is `true` |
| `cleanup` | end of run | ship state file (stamp) |
| `cleanup-pipeline` | end of run | ship state file (stamp), deletes stale state files and run directories |
| `gc` | housekeeping | deletes stale state files unless `detail.dryRun` |
| `init` | housekeeping (legacy) | new ship state file |
| `migrate` | housekeeping | renames state files |
| `history_record` | history store | `.sdlc-v2/history/runs.jsonl` |
| `deferred_add` | history store | `.sdlc-v2/history/deferred.json` |
| `deferred_list` | history store | nothing |
| `deferred_propose_followups` | history store | nothing |
| `deferred_resolve` | history store | `.sdlc-v2/history/deferred.json` |
| `log-cli` | evidence | `.sdlc-v2/evidence/cli-executions.jsonl` |

#### Scenario: Unknown action
- **WHEN** the call passes `action:"bogus"`
- **THEN** the tool returns a `DomainError`
- **AND** the suggestion names every action in the table

### Requirement: Common inputs and state lookup
The tool SHALL resolve the ship state file for `detail.branch`, or for the current git branch when `detail.branch` is absent, as the newest `.sdlc-v2/runs/ship-<branch-slug>-<timestamp>.json` under the main worktree root.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `action` | string | yes | enum, see the actions table | Operation to run. |
| `step` | string | for `begin-step`, `complete-step`, `start`, `complete`, `skip`, `fail`, `decide` | plain text, e.g. `"review"` | Step name. |
| `detail` | object | no | JSON object, e.g. `{"branch":"feat/x"}` | Action-specific fields; all action parameters travel here. |
| `sessionId` | string | no | plain text | Stamped into the state by `init`. |
| `detail.branch` | string | no | plain text branch name | Branch whose state to use. |
| `detail.stateFile` | string | no | absolute path | Read this file directly. Honored only by `begin-step`, `complete-step`, `next`, `todos`. |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Required `step` missing | `DomainError` | `<action>: step is required` |
| Branch cannot be resolved | `DomainError` | `could not determine branch: ...` / pass the branch explicitly |
| No state file for the branch | `DataError` | `no ship state found for branch "<b>"` |
| `step` not in the state's `steps[]` | `DataError` | `step "<s>" not found in state` / run `read` for valid names |
| `detail.stateFile` does not exist | `DataError` | `state file not found: <path>` |
| `detail.stateFile` is not valid JSON | `DomainError` | `failed to parse state file <path>: ...` |
| `detail.stateFile` cannot be read | `InfraError` | `read state file <path>: ...` |
| State file write fails | `InfraError` | `write ship state to <path>: ...` |

- Every input-validation message starts with the action name.
- A failed `step` lookup leaves the state file unchanged.

#### Scenario: Step missing
- **WHEN** the call passes `action:"skip"` with no `step`
- **THEN** the tool returns a `DomainError` naming the `step` field

#### Scenario: No state for branch
- **WHEN** the call passes `action:"read"` and `detail.branch:"feat/none"` has no state file
- **THEN** the tool returns a `DataError` whose message names `feat/none`

#### Scenario: stateFile bypasses branch lookup
- **WHEN** the call passes `action:"next"` and `detail.stateFile` pointing at a valid state file
- **THEN** the tool reads that file without resolving a branch

#### Scenario: Unknown step name
- **WHEN** the call passes `action:"begin-step"` and `step:"bogus"`
- **THEN** the tool returns a `DataError` naming `bogus`
- **AND** the state file bytes are unchanged

### Requirement: Behavior with no ship state
The tool SHALL handle a branch with no ship state file per action as follows.

| Action | Result with no state file |
|---|---|
| `begin-step`, `complete-step`, `start`, `complete`, `skip`, `fail`, `decide`, `read`, `next`, `todos`, `report` | `DataError` naming the branch |
| `defer` | Succeeds; records the finding only in `.sdlc-v2/history/deferred.json` |
| `healing_record` | Succeeds with `written:false`; writes nothing |
| `harden_clusters` | Succeeds; every cluster has `alreadyHardened:false` |
| `cleanup` | Succeeds with an empty result |
| `cleanup-pipeline` | Succeeds with `currentRun.reason:"no-state-file"`; still runs the GC sweep |

#### Scenario: defer without state
- **WHEN** the call passes `action:"defer"` with valid fields on a branch with no state file
- **THEN** the call succeeds
- **AND** no run-scoped entry is written

### Requirement: Step lifecycle
The tool SHALL move a `steps[]` entry between `pending`, `in_progress`, `completed`, `skipped`, and `failed` only through the lifecycle actions, and SHALL NOT check the target step's own current status before changing it.

Status transitions of one `steps[]` entry:

```mermaid
stateDiagram-v2
    [*] --> pending: ship_prepare seeds the entry
    pending --> in_progress: begin-step
    pending --> skipped: skip
    in_progress --> completed: complete-step with outcome success
    in_progress --> failed: fail, or complete-step with outcome failure
    failed --> in_progress: begin-step on resume
    completed --> [*]: cleanup stamps the run
    skipped --> [*]: cleanup stamps the run
    failed --> [*]: cleanup stamps the run
```

| Action | New `status` | Fields set |
|---|---|---|
| `begin-step`, `start` | `in_progress` | `startedAt` |
| `complete-step` (outcome `success`), `complete` | `completed` | `completedAt`; `result` when `detail.result` is present |
| `complete-step` (outcome `failure`) | `failed` | `error` from `detail.result` when present; no `completedAt` |
| `skip` | `skipped` | `completedAt`; `reason` when `detail.reason` is present |
| `fail` | `failed` | `error` when `detail.error` is present; no `completedAt` |

- `decide` never changes any `steps[]` entry.

#### Scenario: Failure outcome stores result as error
- **WHEN** the call passes `action:"complete-step"`, `detail.outcome:"failure"`, and `detail.result:"boom"`
- **THEN** the step's `status` is `failed` and its `error` is `"boom"`

#### Scenario: Retry after failure
- **WHEN** a step is `failed` and the call passes `action:"begin-step"` for it
- **THEN** the step's `status` becomes `in_progress`

### Requirement: Proceed gate on begin-step
The `begin-step` action SHALL return a `DomainError` when any earlier entry in `steps[]` blocks progress.

- A `pending` entry blocks unless it has a `condition` key.
- An `in_progress` or `failed` entry always blocks.
- A `completed` or `skipped` entry never blocks.
- Message: `cannot begin step "<s>" — prior step(s) not terminal-OK: <name>=<status>, ...; complete or skip the blocking step(s) first`.
- The legacy `start` action has no gate.

#### Scenario: Pending predecessor blocks
- **WHEN** `execute` is `pending` without `condition` and the call begins `commit`
- **THEN** the tool returns a `DomainError` naming `execute=pending`

#### Scenario: Skipped predecessor
- **WHEN** `execute` is `skipped` and the call begins `commit`
- **THEN** `commit` becomes `in_progress`

#### Scenario: Conditional pending predecessor
- **WHEN** an earlier entry is `pending` with a `condition` key
- **THEN** it does not block `begin-step`

#### Scenario: Failed predecessor blocks
- **WHEN** `execute` is `failed` and the call begins `commit`
- **THEN** the tool returns a `DomainError` naming `execute=failed`

### Requirement: Step narration output
The lifecycle actions and `decide`/`defer` SHALL return a narration whose fields depend on the action, as the table says.

| Field | Meaning |
|---|---|
| `summary` | One line, e.g. `Step '<s>' started (<pos> of <total>).`, `Step '<s>' completed in <dur> (<pos> of <total>).`, `Step '<s>' failed (<pos> of <total>).`, `Step '<s>' skipped (<pos> of <total>).`, `Decision recorded for step '<s>'.` |
| `display` | Markdown progress block of every step. All these actions; see the detail-level requirement. `defer` with no state file never returns it. |
| `timing` | `{stepSeconds, pipelineSeconds, idleSeconds, human}`; `complete-step`/`complete` only, when computable. |
| `next` | `{id, instruction, etaSeconds, etaBasis}`; `begin-step`, `complete-step`, `start`, `complete` only. `instruction` is `Dispatch the <step> sub-skill.` |
| `todos` | `begin-step`/`complete-step` only: task list (see `todos`). |
| `alreadyDone` | `begin-step` only: `true` when `sideEffects.<step>` exists in the state. |
| `issueCount`, `issueHighlights` | `complete-step` only, when `issues[]` is non-empty: total count and the last 5 issues as `[severity] summary` lines. |

- `begin-step`'s `next.id` is the step it began.
- `complete-step`'s `next` is the first blocking step; it is absent when no step blocks.
- `etaSeconds`/`etaBasis` appear when `.sdlc-v2/timings.json` has samples for `ship:<step>`.

#### Scenario: alreadyDone from journal
- **WHEN** the state holds `sideEffects.pr` and the call begins `pr`
- **THEN** `alreadyDone` is `true`

#### Scenario: No next at pipeline end
- **WHEN** `complete-step` finishes the last blocking step
- **THEN** `next` is absent

### Requirement: Step timing records
On `complete-step` and `complete`, the tool SHALL record the step's duration in `.sdlc-v2/timings.json` under key `ship:<step>`, except for `await-remote-review`.

#### Scenario: Duration recorded
- **WHEN** `review` completes with both `startedAt` and `completedAt` set
- **THEN** `.sdlc-v2/timings.json` holds a sample under `ship:review`

#### Scenario: Human wait step not recorded
- **WHEN** `await-remote-review` completes
- **THEN** no sample is recorded under `ship:await-remote-review`

### Requirement: complete-step outcome input
The `complete-step` action SHALL accept `detail.outcome` of `"success"` (default) or `"failure"` and SHALL return a `DomainError` for any other value.

- Message: `complete-step: outcome must be "success" or "failure", got <value>`.

#### Scenario: Invalid outcome
- **WHEN** the call passes `detail.outcome:"done"`
- **THEN** the tool returns a `DomainError`

### Requirement: fail records an issue
The `fail` action SHALL set `lastFailedStep` to the step name and append one entry to `issues[]`.

```text
{step:<s>, severity:"error", category:"ship-fail", summary:"Step <s> failed", detail:<detail.error as text>, timestamp:<RFC3339 UTC>}
```

- Only `detail.error` is read; a non-string `detail.error` is stored as given and rendered as text in `detail`.
- A state file with no `issues` key gets one.

#### Scenario: Fail appends issue
- **WHEN** the call passes `action:"fail"`, `step:"review"`, `detail.error:"timeout"`
- **THEN** `lastFailedStep` is `"review"`
- **AND** `issues[]` ends with `{step:"review", severity:"error", category:"ship-fail", summary:"Step review failed", detail:"timeout"}`

### Requirement: Narration detail level
For `begin-step`, `complete-step`, `start`, `complete`, `skip`, `fail`, `decide`, and `defer`, the tool SHALL accept `detail.detail` of `"full"` (default) or `"concise"`, and SHALL return a `DomainError` for any other string.

- `"concise"` omits `display` on `skip`, `fail`, `decide`, `defer`.
- `begin-step`, `complete-step`, `start`, `complete` always include `display`.

#### Scenario: Concise skip
- **WHEN** the call passes `action:"skip"` with `detail.detail:"concise"`
- **THEN** the response has no `display`

#### Scenario: Invalid detail level
- **WHEN** the call passes `detail.detail:"verbose"`
- **THEN** the tool returns a `DomainError`

### Requirement: decide
The `decide` action SHALL append `{step, decision}` to `decisions[]`, with `decision` taken from `detail.text`, without checking `step` against `steps[]`.

#### Scenario: Decision for a step with no entry
- **WHEN** the call passes `action:"decide"`, `step:"received-review"`, `detail.text:"fixed 3"`
- **THEN** `decisions[]` ends with `{step:"received-review", decision:"fixed 3"}`

### Requirement: defer
The `defer` action SHALL record one deferred finding in the run's `deferredFindings[]` and in `.sdlc-v2/history/deferred.json`, and SHALL name the generated id in `summary`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `detail.severity` | string | yes | `critical` \| `high` \| `medium` \| `low` \| `info`, case-insensitive | Stored lowercase. |
| `detail.file` | string | yes | plain text path | Finding's file. |
| `detail.title` | string | yes | plain text | Finding's title. |
| `detail.line` | int | no | JSON integer | Finding's line. |
| `detail.reason` | string | no | `below-threshold` \| `needs-direction` \| `disagree` \| `wont-fix` | Defaults to `below-threshold`. |
| `detail.description` | string | no | plain text | Deferring agent's reasoning. Defaults to `title`. |
| `detail.source` | string | no | plain text, e.g. `"received-review"` | Defaults to `review-below-threshold`. |

- Run entry: `{severity, file, line, title, reason, description}`.
- History entry: `{id, created, source, priority, description, status:"open", severity, file, line, reason}`.
- `id` is `review-deferred-<RFC3339 timestamp>-<N>`; `N` is the run's deferred count after this entry, or the history entry count plus 1 with no state file.
- `priority`: `critical`/`high` → `high`; `medium` → `medium`; `low`/`info` → `low`.
- A history entry with the same `id` already present is not written again.
- Success appends ` Written to <path>.` to `summary`.
- A failed history write does not fail the call; `summary` appends a `WARNING: could not persist this item to .sdlc-v2/history/deferred.json (...)` sentence that names the `deferred_add` recovery call with `detail.id` and `detail.description`, and says not to repeat the original call.
- A JSON `null` value counts as omitted.

| Condition | Class | Message (short) |
|---|---|---|
| `severity`, `file`, or `title` missing or empty | `DomainError` | `defer: severity, file, and title are required` |
| Unknown severity | `DomainError` | `defer: detail.severity "<s>" is not a recognised review severity — accepted values are critical \| high \| medium \| low \| info` |
| Unknown reason | `DomainError` | `defer: detail.reason "<r>" is not a recognised deferral reason — accepted values are below-threshold \| needs-direction \| disagree \| wont-fix` |
| `reason`, `description`, or `source` not a string | `DomainError` | `defer: detail.<key> must be a string, got <type>` |
| `line` not an integer | `DomainError` | `defer: detail.line must be an integer, got <type>` |
| History list fails with no state file | `InfraError` | `list deferred issues: ...` |

#### Scenario: Deferred with defaults
- **WHEN** the call passes `detail:{severity:"LOW", file:"a.go", title:"t"}` on a branch with state
- **THEN** the run entry has `severity:"low"`, `reason:"below-threshold"`, `description:"t"`
- **AND** `.sdlc-v2/history/deferred.json` gains an open entry with `source:"review-below-threshold"` and `priority:"low"`

#### Scenario: Source override
- **WHEN** the call passes `detail.source:"received-review"`
- **THEN** the history entry's `source` is `"received-review"`

#### Scenario: Persist failure named
- **WHEN** the history write fails
- **THEN** the call succeeds
- **AND** `summary` contains `WARNING: could not persist this item` and `deferred_add`

### Requirement: healing_record
The `healing_record` action SHALL validate one record of `detail.kind` `review-total`, `fixed`, or `hardened`, and SHALL write it to the live run's `healing` object.

| `detail.kind` | Required fields | Write rule |
|---|---|---|
| `review-total` | `total`, `dimensions` (non-negative integers) | Replaces `healing.reviewTotal`. |
| `fixed` | `origin` (`local-review` \| `pr-comment`), `severity` (review severity, stored lowercase), `file`, `title`; optional integer `line` | Appends to `healing.fixed`; a record with the same `(origin, file, line, title)` is a duplicate. |
| `hardened` | `phase` (`started` \| `done`), `trigger`, `classification`, `applied` (array of `{surface, action, targetFile}`, may be empty), `skipped` (non-negative integer) | Upserts `healing.hardened` by `trigger`: `done` replaces a stored `started`; any other repeat is a duplicate. |

- `applied[].surface` is one of `plan-guardrails`, `execute-guardrails`, `review-dimensions`, `copilot-instructions`, `error-report-skill`, `skill-recommendation`.
- Each record gets `recordedAt`.
- Input is validated before the state lookup.
- With no state file, or a state with `pipelineCompletedAt` set, the call succeeds and writes nothing.

| Output field | Meaning |
|---|---|
| `summary` | `healing_record <kind>: <narration>`; narration is `recorded`, `replaced started record`, `already recorded — no change`, or `no live ship run on this branch — healing not recorded`. |
| `kind` | The kind. |
| `written` | `true` only when this call changed the state file. |
| `record` | The validated record as persisted, `recordedAt` included. |

| Condition | Class | Message (short) |
|---|---|---|
| `kind` missing | `DomainError` | `healing_record: detail.kind is required — accepted values are review-total \| fixed \| hardened` |
| Unknown `kind`, `origin`, `severity`, or `phase` | `DomainError` | `healing_record: detail.<field> "<v>" is not a recognised ... — accepted values are ...` |
| Unknown `applied[].surface` | `DomainError` | `healing_record: detail.applied[<i>].surface "<s>" is not a harden surface id — accepted values are ...` |
| Required field missing | `DomainError` | `healing_record: detail.<field> is required for kind "<kind>"` |
| Integer field negative or fractional | `DomainError` | `healing_record: detail.<field> must be a non-negative integer, got ...` |

#### Scenario: Fixed duplicate
- **WHEN** the same `fixed` record is sent twice
- **THEN** the second response has `written:false` and narration `already recorded — no change`

#### Scenario: Hardened done replaces started
- **WHEN** a `started` record exists for trigger `T` and a `done` record for `T` is sent
- **THEN** narration is `replaced started record` and `healing.hardened` has one entry for `T`

#### Scenario: No live run
- **WHEN** the state has `pipelineCompletedAt` set
- **THEN** the call succeeds with `written:false` and narration `no live ship run on this branch — healing not recorded`

### Requirement: harden_clusters
The `harden_clusters` action SHALL group `detail.findings` into at most 5 clusters keyed by file, without writing any state.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `detail.findings[].file` | string | yes | non-empty path | Cluster key. |
| `detail.findings[].severity` | string | yes | review severity, case-insensitive | Stored lowercase. |
| `detail.findings[].verdict` | string | yes | `agree-will-fix` \| `agree-won't-fix` \| `disagree` \| `needs-direction` | Reviewer verdict. |
| `detail.findings[].title`, `.body` | string | no | plain text | Rendered into `failureText`. |
| `detail.findings[].reason` | string | no | deferral reason | `below-threshold` drops the finding. |

- Findings with `reason:"below-threshold"` are dropped.
- A file whose only finding is a disagree (`verdict:"disagree"` or `reason:"disagree"`) goes to `loneDisagree`.
- Clusters sort by finding count (most first), then file path; files past the fifth go to `suppressed`, sorted.
- `failureText` is `[<severity>] <title> — <verdict>\n<body>` per finding, joined by a blank line; every `"` becomes `'` and every `\` becomes `/`; capped at 4096 characters.
- `alreadyHardened` is `true` when the first 200 characters of `failureText` equal a `healing.hardened[].trigger` of either phase in the branch's state.
- `dirtySurfaces` lists which of `.sdlc-v2/config.toml`, `.sdlc-v2/review-dimensions`, `.github/instructions` have uncommitted changes in the active worktree (`git status --porcelain`).

| Output field | Meaning |
|---|---|
| `clusters` | `[{key, findings, failureText, alreadyHardened}]` |
| `suppressed`, `loneDisagree`, `dirtySurfaces` | Path lists; `[]` never `null`. |
| `narration` | `harden_clusters: <n> cluster(s), <s> suppressed, <l> lone-disagree file(s), <d> dirty surface(s).` |
| `next` | `no clusters — skip`, `all clusters already hardened — commit leftover edits`, or `dispatch <n> cluster(s) with alreadyHardened:false, one at a time` |

| Condition | Class | Message (short) |
|---|---|---|
| `findings` missing or not an array | `DomainError` | `harden_clusters: detail.findings is required` / `must be an array` |
| Bad finding field | `DomainError` | `harden_clusters: detail.findings[<i>].<field> ...` |
| `git status` fails | `DomainError` | `harden_clusters: git status failed: ...` |

#### Scenario: Quote-safe failure text
- **WHEN** a finding title contains `"` and `\`
- **THEN** `failureText` contains `'` and `/` in their place

#### Scenario: Cap at five
- **WHEN** findings span 6 files with 1 finding each
- **THEN** `clusters` has 5 entries in alphabetical order
- **AND** `suppressed` holds the sixth file

#### Scenario: Git status failure
- **WHEN** `git status --porcelain` fails
- **THEN** the tool returns a `DomainError`, never an empty `dirtySurfaces`

### Requirement: read returns state and report data
The `read` action SHALL return the full state object plus a computed `reportData` object, and SHALL NOT modify the state file.

| `reportData` field | Meaning |
|---|---|
| `version` | Always `""`. |
| `bump`, `bumpSource` | `flags.bump` and `sources.bump`. |
| `preRelease`, `preReleasePolicy` | From the state's `versionCfg`; omitted when empty. |
| `stepsTotal`, `stepsCompleted`, `stepsSkipped`, `stepsFailed` | Counts over `steps[]`. |
| `stepsPending` | Count of every other status, `in_progress` included. |
| `duration` | Humanized `startedAt` → `pipelineCompletedAt`, or → now. |
| `decisions` | `"<step>: <decision>"` strings. |
| `deferredFindings` | Count of `deferredFindings[]`. |
| `binaryVersion` | String fields of the state's `binaryVersion`. |
| `reviewLedger` | `{total, fixed, deferredByReason, unaccounted}` or `null`. |
| `reviewLedgerNote` | `review did not run or its total was not recorded`; only when `reviewLedger` is `null`. |
| `healing` | The state's `healing` object, or `{}`. |

- `reviewLedger.total` is `healing.reviewTotal.total`; `null` when that is not an integer.
- `reviewLedger.fixed` counts only `healing.fixed` records with `origin:"local-review"`.
- `reviewLedger.deferredByReason` counts `deferredFindings[]` by `reason`; a missing reason counts as `below-threshold`.
- `reviewLedger.unaccounted` is `total - fixed - deferred` and is never clamped.

#### Scenario: Ledger with a negative gap
- **WHEN** `reviewTotal.total` is 2, one `local-review` fix and two deferrals exist
- **THEN** `reviewLedger.unaccounted` is `-1`

#### Scenario: No review total
- **WHEN** no `review-total` record exists
- **THEN** `reportData.reviewLedger` is `null`
- **AND** `reportData.reviewLedgerNote` is `review did not run or its total was not recorded`

### Requirement: read attaches a resume briefing
The `read` action SHALL attach `resumeBriefing` when some step blocks progress and at least one step has started.

- "Blocks" uses the proceed-gate rule: `pending` without `condition`, `in_progress`, or `failed`.
- The last step is the `in_progress` entry, else the last entry with `startedAt`.
- A `failed` last step still reports `resumable:true`; `read` never errors for it.
- A freshly initialized run (nothing started) has no `resumeBriefing`.

| Field | Meaning |
|---|---|
| `resumable` | Always `true`. |
| `lastStep`, `lastStepStatus` | Name and status of the last step. |
| `sideEffects` | Sorted `"<step> (<kind>): <ref>"` lines from `sideEffects`; `sha` refs shortened to 7 characters; `[]` when none. |
| `summary` | `Run resumable: last step "<s>" (<status>), interrupted <duration> ago.` |
| `display` | Markdown block starting `**Resume briefing**`. |
| `timing` | `{stepSeconds, pipelineSeconds, idleSeconds, human}`; idle time counts from the last step's `completedAt`, else its `startedAt`. |
| `next` | `{id, instruction, etaSeconds, etaBasis}` for the first blocking step. |

#### Scenario: Failed step is resumable
- **WHEN** `execute` is `failed` with `startedAt` 5 minutes ago and `sideEffects.execute` holds a 40-char sha
- **THEN** `resumeBriefing.resumable` is `true` and `lastStepStatus` is `"failed"`
- **AND** `timing.stepSeconds` and `timing.idleSeconds` are `300`
- **AND** `sideEffects` has one entry with the sha shortened to 7 characters

#### Scenario: Fresh run has no briefing
- **WHEN** every step is `pending` with no `startedAt`
- **THEN** the response has no `resumeBriefing`

### Requirement: next
The `next` action SHALL return the first `steps[]` entry that blocks progress as `step`, with its automation mode as `automation`.

- `automation` is the config `automation.steps.<step>` value when set; else `"auto"` under `automation.mode = "unattended"`; else `"confirm"`.
- A config read failure or a missing `automation` section yields `"confirm"`.
- When no step blocks, the result has neither `step` nor `automation`.
- Entries that are not objects are skipped.

#### Scenario: Unattended mode
- **WHEN** `.sdlc-v2/local.toml` sets `[automation] mode = "unattended"`
- **THEN** `automation` is `"auto"`

#### Scenario: Pipeline complete
- **WHEN** every entry is `completed`, `skipped`, or conditional `pending`
- **THEN** the result has no `step`

### Requirement: todos
The `todos` action SHALL return a task list built from `flags.steps` plus a trailing `cleanup` entry, with each step expanded into fixed sub-step items.

- Item `content` is `<Step name with dashes as spaces, first letter upper>: <sub-step>`; `activeForm` is the sub-step with its first letter upper.
- A `completed` step's items are `completed`; `skipped` and `failed` steps are also `completed`, with ` (skipped)` or ` (failed)` appended to `content`.
- For the `step` input, the first item is `in_progress` and the rest are `pending`, unless that step is already terminal.
- Any other `in_progress` step's items are `in_progress`; everything else is `pending`.
- Only names in `flags.steps`, plus `cleanup`, appear.

#### Scenario: Current step
- **WHEN** the call passes `action:"todos"`, `step:"execute"`, and `execute` is `in_progress`
- **THEN** the first `Execute:` item is `in_progress`

### Requirement: cleanup
The `cleanup` action SHALL validate that no `steps[]` entry is `in_progress` or `pending` without `condition`, then stamp `pipelineStatus:"completed"` and `pipelineCompletedAt` and keep the file.

- Success returns `{valid:true, cleaned:true, pipelineStatus:"completed", pipelineCompletedAt}`.
- `failed` entries never violate the contract.
- A violation returns a `DataError` and leaves the file untouched: `pipeline contract violation: <n> step(s) not in terminal state (<name>=<status>, ...) — state file preserved`.

#### Scenario: Valid stamp
- **WHEN** every entry is `completed`, `skipped`, or `failed`
- **THEN** the state file still exists with `pipelineStatus:"completed"`

#### Scenario: Violation preserves file
- **WHEN** an entry is `in_progress`
- **THEN** the tool returns a `DataError` and the file is unchanged

### Requirement: cleanup-pipeline
The `cleanup-pipeline` action SHALL settle the current run, then run a GC sweep and a per-run-directory reap, unless the contract check fails.

| Path | `currentRun` |
|---|---|
| `detail.force:true` | `{cleaned:false, preservedReason:"force"}`; no check, no stamp |
| No state file | `{valid:true, cleaned:false, reason:"no-state-file"}` |
| Valid contract | `{valid:true, cleaned:true, pipelineStatus:"completed", pipelineCompletedAt}`; file stamped |
| Violation | `DataError` as in `cleanup`; no sweep runs |

| Output field | Meaning |
|---|---|
| `currentRun` | See the table above. |
| `gc` | `{ship, execute, plan, commit}`, each `{deleted, kept}`. |
| `directories` | Reap result for stale per-run directories under `.sdlc-v2/runs/`. |
| `force`, `ttlDays` | Resolved inputs. TTL: `detail.ttlDays` > config `state.gc.ttlDays` > `7`. |
| `issueSummary` | `{total, byCategory, items, display, hardenSuggestion?}`; only after a stamp, and only when `issues[]` is non-empty. |

| Condition | Class | Message (short) |
|---|---|---|
| `detail.force` not a boolean | `DomainError` | `cleanup-pipeline: detail.force must be a boolean, got <type>` |
| Sweep fails after a stamp | `InfraError` | `run is already marked completed; only the gc sweep over <dir> failed: ...` / call `ship_state gc` to retry the sweep |
| Sweep fails without a stamp | `InfraError` | `gc sweep over <dir>: ...` |

#### Scenario: Force preserves the run
- **WHEN** the call passes `detail.force:true` with an `in_progress` step
- **THEN** `currentRun` is `{cleaned:false, preservedReason:"force"}`
- **AND** the state file is not stamped

#### Scenario: Issue summary after stamp
- **WHEN** the run stamps and `issues[]` has entries
- **THEN** the response has `issueSummary`

#### Scenario: No issues
- **WHEN** the run stamps and `issues[]` is empty
- **THEN** the response has no `issueSummary`

### Requirement: gc
The `gc` action SHALL prune stale state files, or with `detail.dryRun:true` only classify them.

- TTL: `detail.ttlDays` > config `state.gc.ttlDays` (integer ≥ 0) > `7`; `0` is literal.
- Real run returns `{ttlDays, ship, execute, plan, commit}`, each `{deleted, kept}`; explore tempdirs are also swept but not reported.
- Dry run returns `{dryRun:true, ttlDays, ship, execute, plan}`, each `{wouldDelete, wouldKeep}` of `{file, branch, reason}`; `commit` files are not classified.
- Dry-run reason: `ttl-fresh` (file mtime within TTL) → keep; else `branch-exists` (local branch exists) → keep; else `stale+branch-gone` → would delete.
- `detail.dryRun` that is not a boolean returns a `DomainError` (`gc: detail.dryRun must be a boolean, got <type>`); a top-level `dryRun` is ignored.

#### Scenario: Mistyped dryRun
- **WHEN** the call passes `detail.dryRun:"true"`
- **THEN** the tool returns a `DomainError` and deletes nothing

#### Scenario: Dry run skips commit files
- **WHEN** a stale `commit-*.json` file exists and the call passes `detail.dryRun:true`
- **THEN** the result has no `commit` bucket

### Requirement: init (legacy)
The `init` action SHALL create a new ship state file with the fixed legacy scaffold and delete other ship state files for the same branch slug.

- Seeded: `version:1`, `startedAt`, `branch`, `worktree`, `sessionId`, `flags` (`detail.flags` or `{}`), `decisions:[]`, `deferredFindings:[]`.
- `steps`: `execute`, `commit`, `review`, `received-review` (`condition:"if critical/high findings"`), `commit-fixes` (`condition:"if received-review made changes"`), `pr`; all `pending`, no `kind`.
- Output: `{filePath, prunedOrphans, worktree}`.

#### Scenario: Legacy scaffold
- **WHEN** the call passes `action:"init"` with `detail.branch:"feat/x"`
- **THEN** the new file's `steps` has 6 entries and `received-review` carries a `condition`

### Requirement: migrate
The `migrate` action SHALL rename every state file under `.sdlc-v2/runs/` whose branch slug equals the slug of `detail.from` to use the slug of `detail.to`.

- Returns `{migrated:true}` on success, even when no file matched.
- The `branch` value inside each file is not changed.
- Missing `from` or `to` returns a `DomainError` `migrate: from and to are required`; a rename failure returns an `InfraError`.

#### Scenario: Rename
- **WHEN** `ship-<old-slug>-20200101T000000Z.json` exists and the call migrates old → new
- **THEN** `ship-<new-slug>-20200101T000000Z.json` exists and the old file does not

### Requirement: report
The `report` action SHALL compose the end-of-run report for the branch's ship run and render it, and SHALL write it to `.sdlc-v2/reports/ship-<runId>-report.<md|json>` only when `detail.write` is `true`.

- `detail.format` (`md` \| `json`) is validated first; the default is config `automation.report.format`, else `md`.
- Config `automation.report.enabled = false` returns `{skipped:true}` before any state read.
- A config read error other than not-found uses the defaults and adds an `issues` warning with `category:"cross-read"`.
- `runId` is the run's `startedAt` with every character except digits and `T` removed (e.g. `20260327T143000`).
- `execution` and `guardrailHits` come from the branch's execute state, only when the `execute` step is `completed`.
- `plan` is the latest `plan` record in `.sdlc-v2/history/runs.jsonl` (last 100) whose plan file equals the execute state's plan path; else `null` with `planNote` `no plan linked to this run`, or `plan history could not be read`.
- `cliEvidence` is the branch's `.sdlc-v2/evidence/cli-executions.jsonl` entries since the run's `startedAt`.
- It works on a stamped state and never writes the state file.

| Output field | Meaning |
|---|---|
| `branch`, `runId`, `format`, `bump`, `duration` | Run identity and summary. |
| `plan`, `planNote` | Plan timing `{planFile, startedAt, lastModifiedAt, durationMs}` or `null` with a note. |
| `steps`, `issues`, `decisions` | Step timings, state issues plus cross-read warnings, decision lines. |
| `reviewLedger`, `reviewLedgerNote`, `healing` | As in `read`'s `reportData`. |
| `deferredFindings` | The state's `deferredFindings[]` entries. |
| `hardenCommit` | The `harden` step's `result` when that step is `completed`. |
| `execution`, `guardrailHits`, `cliEvidence`, `linkedLearnings` | Cross-read data. |
| `display` | `md`: the full Markdown report, emitted raw. `json`: one line `Ship run <runId> on <branch>: <c>/<n> steps completed, <f> findings fixed, <d> deferred, <g> guardrail hits.` |
| `path`, `written` | Report file path and `true` after a write. |
| `skipped` | `true` only when reports are disabled. |
| `next` | `Report persisted. Show the path to the user; do not write it yourself.` after a write; else `Show display to the user. Pass detail.write:true to persist the report.` |

| Condition | Class | Message (short) |
|---|---|---|
| `detail.format` not `md`/`json` | `DomainError` | `report: unknown detail.format "<f>" (want md or json)` |
| `detail.format` not a string / `detail.write` not a boolean | `DomainError` | `report: detail.<key> must be a ...` |
| No ship state | `DataError` | `no ship state found for branch "<b>"` |
| Report file write fails | `InfraError` | `write report: ...` |

#### Scenario: Disabled
- **WHEN** config sets `automation.report.enabled = false`
- **THEN** the result is `{skipped:true}` and no file is written

#### Scenario: Write markdown
- **WHEN** the call passes `detail.write:true` with format `md`
- **THEN** `.sdlc-v2/reports/ship-<runId>-report.md` holds `display`
- **AND** `written` is `true`

#### Scenario: Stale execute state excluded
- **WHEN** an execute state exists for the branch but this run's `execute` step is not `completed`
- **THEN** the report has no `execution`

### Requirement: history_record
The `history_record` action SHALL append one run record to `.sdlc-v2/history/runs.jsonl` and return `{ok:true, ts}`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `detail.skill` | string | yes | plain text, e.g. `"ship"` | Skill that ran. |
| `detail.outcome` | string | yes | plain text, e.g. `"success"` | Run result; any non-empty string is accepted. |
| `detail.ts` | string | no | RFC3339 | Defaults to now. |
| `detail.branch`, `detail.version` | string | no | plain text | Stored as given. |
| `detail.duration_ms` | int | no | JSON number | Run duration. |
| `detail.steps`, `detail.guardrail_hits`, `detail.deferred_issues` | string[] | no | JSON array of strings | Non-string items are dropped. |

- Missing `detail`, `skill`, or `outcome` returns a `DomainError` before the history directory is touched.
- An append failure returns an `InfraError` naming the `runs.jsonl` path.

#### Scenario: Missing outcome
- **WHEN** the call passes `detail:{skill:"ship"}`
- **THEN** the tool returns a `DomainError` naming `detail.outcome`

### Requirement: Deferred store actions
The `deferred_add`, `deferred_list`, `deferred_propose_followups`, and `deferred_resolve` actions SHALL read and write `.sdlc-v2/history/deferred.json` without needing ship state.

| Action | Input | Output |
|---|---|---|
| `deferred_add` | `detail.id`, `detail.description` required; `detail.created` (default now), `detail.source`, `detail.priority` (default `medium`) | `{ok:true, id}`; appends an `open` entry with no id check |
| `deferred_list` | none | `{issues, openCount}`; `issues` is `[]` when empty |
| `deferred_propose_followups` | none | `{openCount, groups, display}`; `groups` holds open issues keyed by priority |
| `deferred_resolve` | `detail.id` required | `{ok:true, id}`; sets that entry's `status` to `resolved` |

| Condition | Class | Message (short) |
|---|---|---|
| Missing `detail`, `id`, or `description` | `DomainError` | `deferred_add: detail.id is required` / `detail.description is required` |
| Missing `detail.id` on resolve | `DomainError` | `deferred_resolve: detail.id is required` |
| Id not found on resolve | `DomainError` | `deferred_resolve: ... not found` / call `deferred_list` for valid ids |
| Store read or write fails | `InfraError` | names the `deferred.json` path |

#### Scenario: Resolve unknown id
- **WHEN** the call passes `action:"deferred_resolve"` with an id not in the store
- **THEN** the tool returns a `DomainError`

#### Scenario: Empty store
- **WHEN** no `deferred.json` exists and the call passes `action:"deferred_propose_followups"`
- **THEN** `openCount` is `0`

### Requirement: log-cli
The `log-cli` action SHALL append one CLI execution entry to `.sdlc-v2/evidence/cli-executions.jsonl` and return `{ok:true, action:"log-cli"}`.

- Entry: `{ts, pipeline:"ship", step, branch, command, exitCode, outputHead}` from `detail.step`, the resolved branch, `detail.command`, `detail.exitCode` (default `0`), `detail.outputHead`.
- An unresolvable branch returns a `DomainError`; an append failure returns an `InfraError` whose message starts with `log-cli:`.

#### Scenario: Missing exit code
- **WHEN** the call omits `detail.exitCode`
- **THEN** the appended entry has `exitCode:0`
