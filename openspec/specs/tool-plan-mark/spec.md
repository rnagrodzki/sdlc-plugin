# tool-plan-mark Specification

## Purpose
`plan_mark` is an internal MCP tool the plan skill calls to record progress in the current branch's plan state file: integrity markers, structured results, and the step checkpoint used for resume. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Registration and annotations
The tool SHALL be registered as `plan_mark`, marked "INTERNAL — called by sdlc skills only", with annotations `ReadOnly: true`, `Idempotent: false`, `OpenWorld: false`.

| Annotation | Value |
|---|---|
| Title | `Record plan progress marker` |
| ReadOnly | `true` |
| Idempotent | `false` |
| OpenWorld | `false` |

#### Scenario: Tool is listed with its annotations
- **WHEN** an MCP client lists the server's tools
- **THEN** `plan_mark` is listed with title `Record plan progress marker`
- **AND** its `marker` input declares all 9 marker names as a JSON-schema enum

### Requirement: Input and output fields
The tool SHALL accept the input fields below and SHALL return the output fields below on success.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `marker` | string enum | yes | plain text | One of `plan-file`, `skillInvoked`, `guardrailsEvaluated`, `critiqueRan`, `done`, `guardrailResults`, `criticalDecisions`, `checkpoint`, `review-round`, `review-outcome` |
| `path` | string | only for `plan-file` | plain text | Plan document path to record |
| `data` | object | only for `checkpoint` and `review-round` | JSON | Payload for `guardrailResults`, `criticalDecisions`, `checkpoint` and `review-round`; ignored otherwise |
| `runId` | string | no | plain text | Run ID that `plan_prepare` returned, e.g. `plan-main-20261026T084445Z`. The marker goes to that run. Without it, the newest plan run of the current branch is used |

| Field | Meaning |
|---|---|
| `ok` | `true` on success |
| `marker` | The marker that was written |
| `path` | Path of the plan state file that was written |
| `next` | Set only for `checkpoint`, `review-outcome` and `done` |
| `warnings` | `checkpoint`: the step time was not recorded; `done`: the history append failed, or the timing report skipped data |
| `timing` | Set only for `done`: the Step 7 timing report as raw text |
| `stepTime` | Set only for `checkpoint`: `{step, iteration, at, status}`; `status` is `added` (this call stored the entry), `kept` (an earlier entry owns the time; `at` is its time) or `not recorded` (see `warnings`; `at` is empty) |

#### Scenario: Successful timestamp marker
- **WHEN** `plan_mark({marker: "critiqueRan"})` succeeds
- **THEN** the output has `ok: true`, `marker: "critiqueRan"` and `path` set to the plan state file
- **AND** `next` is empty

#### Scenario: Marker with runId
- **WHEN** `plan_mark({marker: "critiqueRan", runId: "plan-main-20261026T084445Z"})` succeeds
- **THEN** `path` ends with `plan-main-20261026T084445Z.json`

### Requirement: Validation order
The tool SHALL validate in a fixed order and SHALL stop at the first failure without writing the plan state file.

Order of checks before any state write:

```mermaid
flowchart LR
    A["marker in enum"] --> B["plan-file has non-empty path"]
    B --> C["checkpoint data valid"]
    C --> D["current branch readable"]
    D --> E["plan state file found: the run runId names, else the newest run of the branch"]
    E --> F["write plan state file"]
    class E changed
    classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
```

#### Scenario: Bad checkpoint data with no plan state file
- **WHEN** `plan_mark({marker: "checkpoint"})` is called with no `data` on a branch that has no plan state file
- **THEN** it returns the `DomainError` `checkpoint needs data {step, iteration, expectedWriters}`
- **AND** it does not report the missing plan state file

### Requirement: Plan state file lookup
With `runId`, the tool SHALL write to `<main-worktree>/.sdlc-v2/runs/<runId>.json`. Without `runId`, it SHALL write to the newest `plan-<branch-slug>-<YYYYMMDDTHHmmssZ>.json` file in `<main-worktree>/.sdlc-v2/runs/`, chosen by the timestamp in the file name, where `<branch-slug>` matches the current branch exactly. Each write SHALL remove the branch's other `plan-<branch-slug>-*.json` files that are not kept.

- A `runId` run that has `planIntegrity.done` is accepted.
- The branch is read from the active worktree.
- The state file always lives under the main worktree.
- A file of another branch whose slug starts with this slug (for example `plan-feat-x-*` on branch `feat`) is never selected or changed.

Keep rule for a sibling run. The writer `sessionId` is the `sessionId` of the run that `plan_mark` writes. The first rule that matches applies:

| Rule | Sibling run | Result |
|---|---|---|
| 1 | has `planIntegrity.done` | kept |
| 2 | state file last written less than 24 hours ago | kept |
| 3 | has a non-empty `sessionId` that differs from the writer `sessionId` | kept |
| 4 | any other run | its state file is removed. Its `.evidence/` directory stays until a `plan_prepare` call that starts a new run, or the GC TTL sweep, removes it. |

- An empty writer `sessionId` (the hook did not stamp the written run yet) counts as different from every non-empty `sessionId`.
- The GC TTL sweep (7 days by default) removes a run that rule 3 keeps, when that run is not the newest run of the branch.

#### Scenario: Exact slug match
- **WHEN** branch `feat` has `plan-feat-20260929T110000Z.json` and a newer `plan-feat-x-20260929T120000Z.json` exists
- **THEN** `plan_mark` writes `plan-feat-20260929T110000Z.json`
- **AND** `plan-feat-x-20260929T120000Z.json` stays byte-identical

#### Scenario: Repeated marks keep one file
- **WHEN** `plan_prepare` created the branch's only state file and `plan_mark` is then called three times
- **THEN** exactly one `plan-<branch-slug>-*.json` file exists for the branch

#### Scenario: runId targets its own run
- **WHEN** branch `main` has live runs A and a newer B
- **AND** `plan_mark({marker: "checkpoint", runId: "<runId of A>", data: {step: "2", iteration: 0, expectedWriters: []}})` is called
- **THEN** the checkpoint is written to the state file of A
- **AND** the state file of B is byte-identical

#### Scenario: Stale sibling removed
- **WHEN** branch `main` has a not-done sibling run whose state file was last written more than 24 hours ago
- **AND** the sibling has no `sessionId`
- **AND** `plan_mark` writes another run of `main`
- **THEN** the stale sibling file is deleted

#### Scenario: Stale sibling of the same session removed
- **WHEN** branch `main` has a not-done sibling run whose state file was last written more than 24 hours ago
- **AND** the sibling has `sessionId` `s-1`
- **AND** `plan_mark` writes another run of `main` whose `sessionId` is `s-1`
- **THEN** the stale sibling file is deleted

#### Scenario: Idle sibling of another session kept
- **WHEN** branch `main` has a not-done sibling run whose state file was last written more than 24 hours ago
- **AND** the sibling has `sessionId` `s-1`
- **AND** `plan_mark` writes another run of `main` whose `sessionId` is `s-2`
- **THEN** the sibling file still exists

#### Scenario: No plan state file
- **WHEN** `plan_mark` is called on a branch where `plan_prepare` never ran
- **THEN** it returns a `DomainError` `no plan state file found for branch "<branch>"; run plan_prepare first`

### Requirement: Timestamp markers
For `plan-file`, `skillInvoked`, `guardrailsEvaluated`, `critiqueRan` and `done`, the tool SHALL set `planIntegrity.<key>` to the current UTC time (RFC 3339) and SHALL ignore `data`.

| Marker | `planIntegrity` key | Extra effect |
|---|---|---|
| `plan-file` | `planFile` | sets top-level `planFilePath` to `path` |
| `skillInvoked` | `skillInvoked` | none |
| `guardrailsEvaluated` | `guardrailsEvaluated` | none |
| `critiqueRan` | `critiqueRan` | none |
| `done` | `done` | appends a history record (see below) |

#### Scenario: plan-file records the path
- **WHEN** `plan_mark({marker: "plan-file", path: "plans/my-plan.md"})` succeeds and that file does not exist
- **THEN** the state file has `planIntegrity.planFile` set to a timestamp
- **AND** `planFilePath` is `plans/my-plan.md`

#### Scenario: Data passed to a timestamp marker
- **WHEN** `plan_mark({marker: "guardrailsEvaluated", data: {results: [...]}})` is called
- **THEN** `planIntegrity.guardrailsEvaluated` is set to a timestamp
- **AND** no `guardrailResults` key is added

### Requirement: Append-only structured markers
For `guardrailResults` and `criticalDecisions`, the tool SHALL append the array in `data` to a top-level state key of the same name and SHALL NOT change `planIntegrity`.

| Marker | Array read from | Entry shape |
|---|---|---|
| `guardrailResults` | `data.results` | `{id, status, detail}` |
| `criticalDecisions` | `data.decisions` | `{key, choice, rejected, reason, at}` |

- A missing or non-array payload appends nothing; the call still succeeds.
- `rejected` is an array of `{option, why}`; a missing `rejected` is stored as `[]`.
- `at` is set by the tool to the call time, RFC 3339 UTC; a caller value is ignored.

#### Scenario: Two guardrailResults calls
- **WHEN** `plan_mark({marker: "guardrailResults", data: {results: [A]}})` is followed by the same call with `[B]`
- **THEN** the state key `guardrailResults` is `[A, B]`
- **AND** `planIntegrity` is unchanged

#### Scenario: Decision with rejected alternatives
- **WHEN** `plan_mark({marker:"criticalDecisions", data:{decisions:[{key:"k", choice:"merge", rejected:[{option:"rebase", why:"rewrites SHAs"}], reason:"r"}]}})` is called
- **THEN** the stored entry has `rejected` `[{option:"rebase", why:"rewrites SHAs"}]` and an `at` timestamp

#### Scenario: Decision without rejected
- **WHEN** a `criticalDecisions` entry has no `rejected`
- **THEN** the stored entry has `rejected: []`

### Requirement: Checkpoint marker replaces the progress checkpoint
For `checkpoint`, the tool SHALL store `{step, iteration, expectedWriters, updatedAt}` at the top-level `checkpoint` key, replacing any previous value.

| `data` key | Rule |
|---|---|
| `step` | Required. One of `0`, `1`, `2`, `3`, `4`, `5`, `6`, `6.5`, `6.6`, `7` (strings) |
| `iteration` | Optional whole number `>= 0`; default `0` |
| `expectedWriters` | Optional JSON array, max 32 entries, each matching `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` |
| any other key | Rejected |

#### Scenario: Second checkpoint replaces the first
- **WHEN** a checkpoint with `step: "2"` is followed by one with `step: "3"`, `iteration: 1`, `expectedWriters: ["lane-static-structural-r1", "lane-content-coverage-r1"]`
- **THEN** `checkpoint` is a single object with `step: "3"` and those two writers
- **AND** `checkpoint.updatedAt` is a non-empty timestamp

### Requirement: Checkpoint data errors
The tool SHALL reject invalid checkpoint `data` with a `DomainError` that has a non-empty suggestion, and SHALL leave the plan state file byte-identical.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `data` missing or empty | `DomainError` | `checkpoint needs data {step, iteration, expectedWriters}` / `call plan_mark with data {step:"3", iteration:1}` |
| Unknown key | `DomainError` | `checkpoint data has unknown key "<k>"` / remove it |
| `step` missing or not in the list | `DomainError` | `checkpoint step "<s>" is not valid — valid steps: 0, 1, 2, 3, 4, 5, 6, 6.5, 6.6, 7` |
| `iteration` negative, fractional or not a number | `DomainError` | `checkpoint iteration must be an integer >= 0` |
| `expectedWriters` not an array | `DomainError` | `checkpoint expectedWriters must be a JSON array of writer IDs` |
| `expectedWriters` over 32 entries | `DomainError` | `checkpoint expectedWriters has <n> entries, max 32` / pass only the current fan-out |
| An entry fails the writer ID pattern | `DomainError` | `checkpoint expectedWriters[<i>] "<id>" is not a valid writer ID` |

#### Scenario: Step outside the list
- **WHEN** `plan_mark({marker: "checkpoint", data: {step: "9"}})` is called
- **THEN** it returns a `DomainError` containing `checkpoint step "9" is not valid`
- **AND** the plan state file is unchanged

#### Scenario: Too many writers
- **WHEN** `expectedWriters` has 33 entries
- **THEN** it returns a `DomainError` containing `checkpoint expectedWriters has 33 entries, max 32`

### Requirement: Checkpoint next text
The `checkpoint` marker SHALL return `next` as shown below. The tool SHALL read the `[planStyle]` config section fresh on every checkpoint call to build it.

| Condition | `next` text |
|---|---|
| Always | `Checkpoint saved at step <s>. Continue step <s>.` |
| `step` is `5` and `iteration` equals the review-loop limit (5) | append ` This is review round 5 of 5, the last round. If blocking issues remain after it, ask the user with AskUserQuestion; do not start round 6.` |
| `[planStyle].instructions` has entries | append a new line `Custom plan instructions (follow them in this step):`, then one line `<n>. <instruction>` per entry |
| `[planStyle]` cannot be read | append ` Warning: Failed to read planStyle config: <cause> — custom plan instructions could not be loaded; fix local.toml.` |

#### Scenario: Instructions added between calls
- **WHEN** `.sdlc-v2/local.toml` gains two `[planStyle].instructions` entries `A` and `B` after a first checkpoint call
- **THEN** the next checkpoint call's `next` ends with `Custom plan instructions (follow them in this step):\n1. A\n2. B`

#### Scenario: Last review round
- **WHEN** `plan_mark({marker: "checkpoint", data: {step: "5", iteration: 5}})` succeeds
- **THEN** `next` contains `This is review round 5 of 5, the last round.`

#### Scenario: Earlier review round
- **WHEN** `plan_mark({marker: "checkpoint", data: {step: "5", iteration: 4}})` succeeds
- **THEN** `next` does not contain `the last round`

#### Scenario: Non-checkpoint marker
- **WHEN** `plan_mark({marker: "critiqueRan"})` succeeds
- **THEN** `next` is empty

### Requirement: Plan timing refresh
On every successful write, the tool SHALL refresh the top-level `planTiming` object `{startedAt, lastModifiedAt, durationMs}` from `planIntegrity.skillInvoked` and the plan file's modification time.

- `lastModifiedAt` is the plan file's modification time, not the time of the call.
- A relative `planFilePath` is resolved against the active worktree root and stored as an absolute, cleaned path.
- When `skillInvoked` or `planFilePath` is missing, or the plan file cannot be stat'ed, `planTiming` and `planFilePath` keep their previous values.
- Timing problems never fail the call.

#### Scenario: Relative plan path is normalized
- **WHEN** `planFilePath` is `docs/plan.md` and that file exists in the active worktree
- **THEN** after any marker call `planFilePath` is the absolute path of that file
- **AND** `planTiming.lastModifiedAt` equals the file's modification time

#### Scenario: Plan file disappears
- **WHEN** the plan file cannot be stat'ed during a marker call
- **THEN** `planTiming` keeps its previous value
- **AND** the call returns `ok: true`

### Requirement: Done marker history record
For `done`, after the state write the tool SHALL append one JSON line to `<main-worktree>/.sdlc-v2/history/runs.jsonl` when `planFilePath` and `planTiming` are both set.

| Record field | Value |
|---|---|
| `ts` | Time of the call |
| `skill` | `plan` |
| `branch` | Current branch |
| `outcome` | `done` |
| `duration_ms` | `planTiming.durationMs` |
| `plan_file` | `planFilePath` |
| `started_at` / `last_modified_at` | From `planTiming` |

- No record is appended when `planFilePath` or `planTiming` is missing.
- Each `done` call appends a new record; earlier records are kept.

#### Scenario: History append fails
- **WHEN** the `done` marker is saved but writing `runs.jsonl` fails
- **THEN** the output has `ok: true`
- **AND** `warnings` holds `plan timing not saved to history: <cause>`

#### Scenario: Done called twice
- **WHEN** `done` is called twice for the same run
- **THEN** `runs.jsonl` has two `skill: "plan"` records, the second one newer

### Requirement: Error cases
The tool SHALL return these errors in addition to the checkpoint data errors.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree cannot be resolved | `InfraError` | `resolve main root: <cause>` / run inside a git repository |
| Active worktree cannot be resolved | `InfraError` | `resolve active root: <cause>` / run inside a git working tree |
| `marker` not in the enum | `DomainError` | `unknown marker "<m>"; must be one of: <sorted list>` / use the exact, case-sensitive name |
| `plan-file` with blank `path` | `DomainError` | `marker "plan-file" requires a non-empty path` |
| Current branch cannot be read (git fails in the active worktree) | `InfraError` | `could not determine current branch` / run from inside a git repository or worktree |
| State file lookup fails | `InfraError` | `find plan state file: <cause>` / check read permission |
| No state file for the branch | `DomainError` | `no plan state file found for branch "<branch>"; run plan_prepare first` / stay on the branch `plan_prepare` ran on |
| `runId` state file cannot be read or is not JSON | `InfraError` | `plan run <runId> state read failed` / `the plan state file is unreadable or corrupt: fix its permissions or delete it, then call plan_prepare again` |
| `runId` is invalid (not a bare `plan-` run ID) or has no state file | `DomainError` | `plan run <runId> not found` / `Pass the runId that plan_prepare returned in this session. After a compaction, use the runId of the "Resume with:" line. If that run no longer exists, call plan_prepare with no runId, no resume and no resolveTemplate to start a new run.` |
| State file write fails | `InfraError` | `write plan state file: <cause>` / check write permission and disk space |

#### Scenario: Not a git repository
- **WHEN** `plan_mark({marker: "guardrailsEvaluated"})` runs in a directory that is not a git repository
- **THEN** it returns `InfraError` `could not determine current branch`
- **AND** the suggestion says to run from inside a git repository or worktree

#### Scenario: Detached HEAD
- **WHEN** `plan_mark` runs on a detached HEAD
- **THEN** the branch reads as `HEAD`, not as a branch error
- **AND** with no plan run for `HEAD` it returns the `no plan state file found for branch "HEAD"` error

#### Scenario: Unknown marker
- **WHEN** `plan_mark({marker: "not-a-real-marker"})` is called
- **THEN** it returns a `DomainError` starting with `unknown marker "not-a-real-marker"; must be one of:`

#### Scenario: plan-file without a path
- **WHEN** `plan_mark({marker: "plan-file", path: ""})` is called
- **THEN** it returns the `DomainError` `marker "plan-file" requires a non-empty path`

#### Scenario: runId of a deleted run
- **WHEN** `plan_mark({marker: "critiqueRan", runId: "plan-main-20261026T084445Z"})` is called
- **AND** `plan-main-20261026T084445Z.json` does not exist
- **THEN** it returns the `DomainError` `plan run plan-main-20261026T084445Z not found`
- **AND** no state file changes

#### Scenario: Path traversal runId
- **WHEN** `plan_mark({marker: "critiqueRan", runId: "../plan-main-20261026T084445Z"})` is called
- **THEN** it returns the `DomainError` `plan run ../plan-main-20261026T084445Z not found`

### Requirement: Plan run survives the done marker
After `planIntegrity.done` is set, the plan run state file and its `<runId>.evidence/` dir SHALL stay on disk until ship's `cleanup-pipeline` deletes them after the report, or until GC removes them by TTL. No Stop hook deletes them.

- A run with `planIntegrity.done` set is never selected as the active plan run.

#### Scenario: Stop after planning
- **WHEN** `plan_mark({marker:"done"})` is called and the session then stops
- **THEN** `.sdlc-v2/runs/plan-<slug>-<ts>.json` and `.sdlc-v2/runs/plan-<slug>-<ts>.evidence/` still exist

#### Scenario: Done run is not resumed
- **WHEN** the only plan run for the branch has `planIntegrity.done` set and `plan_prepare` runs
- **THEN** a new plan run is created

### Requirement: Review-round marker
The `review-round` marker SHALL store one row of `reviewRounds` in the plan state with `round`, `mergedStatus`, `found`, `fixed`, and `lenses`. A row with the same `round` SHALL be replaced. Rows SHALL stay sorted by `round`. The marker SHALL return no `next`.

#### Scenario: First call for a round
- **WHEN** `plan_mark({marker: "review-round", data: {round: 1, mergedStatus: "Approved", found: 0, fixed: 0, lenses: [{name: "all", verdict: "Approved"}]}})` succeeds
- **THEN** the plan state has one `reviewRounds` row with `round: 1`
- **AND** the output has no `next`

#### Scenario: Replayed round
- **WHEN** `reviewRounds` has a row with `round: 2, fixed: 0`
- **AND** a `review-round` call sends `round: 2, fixed: 2`
- **THEN** `reviewRounds` has one row with `round: 2` and `fixed: 2`

### Requirement: Review-round data errors
The tool SHALL reject an invalid `review-round` payload with a `DomainError` that has a `Suggestion`, and SHALL not change the plan state file.

#### Scenario: Bad status
- **WHEN** a `review-round` call sends `mergedStatus: "ok"`
- **THEN** the tool returns a `DomainError` whose Suggestion contains `Approved` and `Issues Found`
- **AND** the plan state file bytes do not change

#### Scenario: Unknown key
- **WHEN** a `review-round` call sends the key `at`
- **THEN** the `DomainError` names `at`
- **AND** its Suggestion lists `round, mergedStatus, found, fixed, lenses`

#### Scenario: Bad round number
- **WHEN** a `review-round` call sends `round: 0`
- **THEN** the tool returns a `DomainError`

#### Scenario: Bad counts
- **WHEN** a `review-round` call sends `found: -1`
- **THEN** the tool returns a `DomainError`

#### Scenario: Too many lenses
- **WHEN** a `review-round` call sends 33 lenses
- **THEN** the tool returns a `DomainError`

#### Scenario: Too many rounds
- **WHEN** the plan state has 20 rounds and a call sends `round: 21`
- **THEN** the tool returns a `DomainError`

### Requirement: Round finding IDs
The `review-round` marker SHALL accept an optional `findings` list of at most 200 `{id, fixed}` entries, where `id` matches `^f-[0-9a-f]{8}$`. A passed empty list SHALL be stored as `[]`. An absent key SHALL not be stored. A bad entry SHALL return a DomainError with a Suggestion and write nothing.

#### Scenario: Round with IDs
- **WHEN** `review-round` gets `findings` `[{id:"f-3a9c1e07", fixed:true}]`
- **THEN** the stored round has that `findings` list

#### Scenario: Old caller
- **WHEN** `review-round` gets no `findings` key
- **THEN** the stored round has no `findings` key

#### Scenario: Bad ID
- **WHEN** a finding has `id` `x1`
- **THEN** the call returns a DomainError with the Suggestion `Pass the id from merge_results allIssues.`

### Requirement: Review outcome marker
The `review-outcome` marker SHALL replace `reviewOutcome` with the full list of each call: `{findings:[{id, text, choice, reason}]}`, with `choice` `accepted`, `rejected`, or `stop`, `text` and `reason` at most 200 characters, and at most 200 findings. A bad input SHALL return a DomainError with a Suggestion and write nothing. The result SHALL set `next`.

#### Scenario: Second call replaces
- **WHEN** call 1 stores findings `a` and `b`
- **AND** call 2 passes only `a`
- **THEN** `reviewOutcome` holds only `a`

#### Scenario: Bad choice
- **WHEN** a finding has `choice` `maybe`
- **THEN** the call returns a DomainError with the Suggestion `Use accepted, rejected, or stop.`

#### Scenario: Empty list
- **WHEN** `data.findings` is empty
- **THEN** the call returns a DomainError
- **AND** the stored outcome does not change

### Requirement: Step times on checkpoint
The `checkpoint` marker SHALL add `{step, iteration, at}` to the top-level `stepTimes` list of the plan state for the first checkpoint of each step and iteration, and SHALL return `stepTime`. It SHALL keep 64 entries or fewer and SHALL keep every other top-level key. At the limit, or when `stepTimes` is not a list, it SHALL still save the checkpoint, add no entry, return `stepTime.status` `not recorded` and return a warning.

#### Scenario: First checkpoint
- **WHEN** `stepTimes` is absent and a checkpoint sends step `2` and iteration `0`
- **THEN** `stepTimes` holds one entry for step `2` iteration `0`
- **AND** `stepTime.status` is `added` and `stepTime.at` is the stored `at`

#### Scenario: Repeated checkpoint
- **WHEN** two checkpoint calls send step `3` and iteration `1`
- **THEN** `stepTimes` holds one entry for step `3` iteration `1`, with the `at` of the first call
- **AND** the second call returns `stepTime.status` `kept` with the `at` of the first call

#### Scenario: Last free entry
- **WHEN** `stepTimes` holds 63 entries and a checkpoint sends a new step and iteration
- **THEN** `stepTimes` holds 64 entries and the checkpoint returns no warning

#### Scenario: Full list
- **WHEN** `stepTimes` holds 64 entries and a checkpoint sends step `3` and iteration `0`
- **THEN** the checkpoint returns `ok: true`, `stepTime.status` `not recorded`, and the warning `Step time not recorded for step 3 iteration 0: stepTimes holds 64 entries, the limit (64). The Steps line of the Step 7 timing report leaves this step out.`

#### Scenario: Value that is not a list
- **WHEN** `stepTimes` holds a string and a checkpoint sends step `2` and iteration `0`
- **THEN** the checkpoint keeps the value, writes the checkpoint, and returns the warning `Step time not recorded for step 2 iteration 0: stepTimes in <state file> is a string, not a list. Expected a list such as [{"step":"2","iteration":0,"at":"2026-10-23T12:00:00Z"}]. The Step 7 timing report shows Steps: unknown. To fix it, remove the stepTimes key from that file.`
- **AND** a later `done` returns the warning that starts with `Timing: stepTimes in <state file> is a string, not a list, so the Steps line shows unknown.`

### Requirement: Done marker timing report
The `done` marker SHALL return `timing`, a raw-text report with the total time, the time of each step and the 3 slowest agents, and `next` `Print timing verbatim. Then continue Step 7 with the plan-mode or normal-mode branch.` It SHALL return ok when timing data is absent or bad. Each warning SHALL start with `Timing: `. The report SHALL read only the first 64 `stepTimes` entries.

#### Scenario: Report lines
- **WHEN** `plan_mark({marker: "done"})` succeeds
- **THEN** `timing` starts with `Step 7: Timing (wall clock, UTC)` and has the lines `Total:`, `Steps:` and `Slowest agents:`

#### Scenario: No start stamp
- **WHEN** `planIntegrity.skillInvoked` is absent
- **THEN** `timing` holds `Total: unknown`

#### Scenario: Evidence folder cannot be listed
- **WHEN** the evidence folder path is a regular file
- **THEN** `done` returns `ok: true`, a warning that starts with `Timing: cannot read evidence:`, and `Slowest agents: unknown`

#### Scenario: Evidence folder missing
- **WHEN** the evidence folder does not exist
- **THEN** `done` returns `ok: true`, no warning, and `Slowest agents: none`

#### Scenario: Four finished writers
- **WHEN** 4 finished writers have `startedAt`
- **THEN** `timing` lists the 3 longest writers and leaves out the shortest

#### Scenario: Skipped writers
- **WHEN** writer `main` is done, one writer has status `running`, and one writer has no `startedAt`
- **THEN** `timing` lists none of these writers

#### Scenario: Bad writer time
- **WHEN** writer `lens-risk-r1` has an `updatedAt` before its `startedAt`
- **THEN** `done` returns the warning `Timing: writer lens-risk-r1 skipped: updatedAt <updatedAt> is before startedAt <startedAt>`

#### Scenario: Writer time not RFC 3339
- **WHEN** writer `bad-time` has `startedAt` `yesterday`
- **THEN** `done` returns the warning `Timing: writer bad-time skipped: startedAt "yesterday" is not RFC 3339 (example: 2026-10-23T12:00:00Z)`

#### Scenario: Step time before the previous step
- **WHEN** the `stepTimes` entry of step `3` has a time before the entry of the step before it
- **THEN** `timing` shows `3 unknown`
- **AND** `done` returns the warning `Timing: step 3 is shown as unknown: its time <T3> is before <T2>, the time of the step before it`

#### Scenario: List past the limit
- **WHEN** `stepTimes` holds 66 entries
- **THEN** the report reads the first 64 entries only
- **AND** `done` returns the warning `Timing: stepTimes holds 66 entries; the report reads the first 64 only.`
