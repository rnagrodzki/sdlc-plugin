# Spec Delta

## MODIFIED Requirements

### Requirement: harden_clusters
The `harden_clusters` action SHALL group `detail.findings` into at most 5 clusters keyed by file, without writing any state.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `detail.findings[].file` | string | yes | non-empty path | Cluster key. |
| `detail.findings[].severity` | string | yes | review severity, case-insensitive | Stored lowercase. |
| `detail.findings[].verdict` | string | yes | `agree-will-fix` \| `agree-won't-fix` \| `disagree` \| `needs-direction` \| `not-evaluated` | Reviewer verdict. `not-evaluated` only with reason `below-threshold`. |
| `detail.findings[].title`, `.body` | string | no | plain text | Rendered into `failureText`. |
| `detail.findings[].reason` | string | no | deferral reason | Decides the output list. |
| `detail.findings[].line` | number | no | line number | Copied to the output lists. |
| `detail.findings[].deferredId` | string | no | deferred item id | Needed for `fixNow`. |
| `detail.findings[].fixedInRun` | bool | no | `true` after a fix-pass fix | Removes the finding from `fixNow`. |

- A finding with `reason:"below-threshold"` joins the cluster of its file only when the file has another finding.
- A file whose only finding is a disagree (`verdict:"disagree"` or `reason:"disagree"`) goes to `loneDisagree`.
- Clusters sort by finding count (most first), then file path; files past the fifth go to `suppressed`, sorted.
- `failureText` is `[<severity>] <title> — <verdict>\n<body>` per finding, joined by a blank line; every `"` becomes `'` and every `\` becomes `/`; capped at 4096 characters. `fixedInRun` does not change it.
- `alreadyHardened` is `true` when `failureText` equals a `healing.hardened[].trigger` of either phase in the branch's state after each is trimmed of surrounding white space, capped at 200 characters, and trimmed again.
- `dirtySurfaces` lists which of `.sdlc-v2/config.toml`, `.sdlc-v2/review-dimensions`, `.github/instructions` have uncommitted changes in the active worktree (`git status --porcelain`).
- `fixNow` holds `below-threshold` findings with a `deferredId` and `fixedInRun` false. `staysDeferred` holds `below-threshold` findings with no `deferredId` and every `needs-direction` finding. `evidenceOnly` holds `disagree` and `wont-fix` findings.

| Output field | Meaning |
|---|---|
| `clusters` | `[{key, findings, failureText, alreadyHardened}]` |
| `suppressed`, `loneDisagree`, `dirtySurfaces` | Path lists; `[]` never `null`. |
| `fixNow`, `staysDeferred`, `evidenceOnly` | Finding lists; `[]` never `null`. |
| `narration` | `harden_clusters: <n> cluster(s), <s> suppressed, <l> lone-disagree file(s), <d> dirty surface(s).` |
| `next` | `no clusters — skip`, `no clusters to harden. Run one fix pass for <m> fix-now finding(s)`, `all clusters already hardened — commit leftover edits`, `dispatch <n> cluster(s) with alreadyHardened:false, one at a time`, or `dispatch <n> cluster(s), then run one fix pass for <m> fix-now finding(s)` |

| Condition | Class | Message (short) |
|---|---|---|
| `findings` missing or not an array | `DomainError` | `harden_clusters: detail.findings is required` / `must be an array` |
| Bad finding field | `DomainError` | `harden_clusters: detail.findings[<i>].<field> ...` |
| `not-evaluated` with another reason | `DomainError` | `harden_clusters: detail.findings[<i>].verdict not-evaluated needs reason below-threshold ...` |
| `git status` fails | `DomainError` | `harden_clusters: git status failed: ...` |

#### Scenario: Trigger without the trailing newline
- **WHEN** a finding has an empty `body`, so its `failureText` ends with a newline
- **AND** the state holds a `healing.hardened[].trigger` equal to that text without the newline
- **THEN** the cluster has `alreadyHardened:true`

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

#### Scenario: First status line keeps its status column
- **WHEN** the only change in the active worktree is an unstaged edit to tracked `.sdlc-v2/config.toml`, so `git status --porcelain` prints ` M .sdlc-v2/config.toml` as its first line
- **THEN** `dirtySurfaces` is `[".sdlc-v2/config.toml"]`

#### Scenario: Below-threshold finding alone
- **WHEN** the only finding is `below-threshold` with `deferredId` `review-deferred-x-1` and verdict `not-evaluated`
- **THEN** `clusters` is empty
- **AND** `fixNow` has 1 entry
- **AND** `next` is `no clusters to harden. Run one fix pass for 1 fix-now finding(s)`

#### Scenario: Fixed finding on resume
- **WHEN** the same input is sent again with `fixedInRun:true` on the fix-now finding
- **THEN** `fixNow` is empty
- **AND** each `failureText` is the same as before

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
| `deferredFindings` | Count of `deferredFindings[]` entries without `fixedInRun:true`. |
| `binaryVersion` | String fields of the state's `binaryVersion`. |
| `reviewLedger` | `{total, fixed, deferredByReason, unaccounted}` or `null`. |
| `reviewLedgerNote` | `review did not run or its total was not recorded`; only when `reviewLedger` is `null`. |
| `healing` | The state's `healing` object, or `{}`. |

- `reviewLedger.total` is `healing.reviewTotal.total`; `null` when that is not an integer.
- `reviewLedger.fixed` counts only `healing.fixed` records with `origin:"local-review"`.
- `reviewLedger.deferredByReason` counts `deferredFindings[]` entries without `fixedInRun:true` by `reason`; a missing reason counts as `below-threshold`.
- `reviewLedger.unaccounted` is `total - fixed - deferred` and is never clamped.

#### Scenario: Ledger with a negative gap
- **WHEN** `reviewTotal.total` is 2, one `local-review` fix and two deferrals exist
- **THEN** `reviewLedger.unaccounted` is `-1`

#### Scenario: No review total
- **WHEN** no `review-total` record exists
- **THEN** `reportData.reviewLedger` is `null`
- **AND** `reportData.reviewLedgerNote` is `review did not run or its total was not recorded`

#### Scenario: Fix-pass fix counts once
- **WHEN** `reviewTotal.total` is 3, all 3 findings are deferred, and one entry has `fixedInRun:true` with its `local-review` fix record
- **THEN** `reviewLedger` is `fixed 1`, deferred 2, `unaccounted 0`

## ADDED Requirements

### Requirement: defer stores the deferred id
The `defer` action SHALL write or merge the deferred item first and SHALL store the returned item id as `deferredId` on the run entry. When the item write fails, the run entry SHALL have no `deferredId` and the summary SHALL carry the persist warning.

#### Scenario: Item written
- **WHEN** `defer` runs with reason `below-threshold` on file `a.go`
- **THEN** the run entry has a `deferredId` that names an open row of `.sdlc-v2/history/deferred.json`

#### Scenario: Same file, needs direction
- **WHEN** two `defer` calls with reason `needs-direction` name file `a.go`
- **THEN** `deferred.json` has one open row for `a.go` with two description lines

### Requirement: healing_record resolves a deferred id
The `healing_record` action with kind `fixed` and `detail.deferredId` SHALL resolve that row in `.sdlc-v2/history/deferred.json` and SHALL set `fixedInRun:true` on the run entry with that id. A row that is already resolved SHALL be a no-op for the store.

#### Scenario: Fix pass fix
- **WHEN** `healing_record` gets kind `fixed` with `deferredId` `review-deferred-x-2`
- **THEN** the row `review-deferred-x-2` has status `resolved`
- **AND** the run entry has `fixedInRun:true`

#### Scenario: Unknown deferred id
- **WHEN** `detail.deferredId` names no run entry
- **THEN** the tool returns a DomainError and writes nothing

### Requirement: Deferred store merge and bounds
`deferred_add` with `merge:true` SHALL append only new lines to an open row with the same id. A store file over 8 MiB or one that does not parse SHALL return a DataError, and every write SHALL be atomic.

#### Scenario: Repeat merge
- **WHEN** `deferred_add` with `merge:true` runs twice with the same id and text
- **THEN** the open row holds the text once

#### Scenario: Resolved row only
- **WHEN** the only row with id `harden-pending-plan` is resolved
- **AND** `deferred_add` runs with that id and `merge:true`
- **THEN** a new row `harden-pending-plan-2` is added

#### Scenario: File too large
- **WHEN** `deferred.json` is larger than 8 MiB
- **THEN** `deferred_add` returns a DataError
- **AND** the file is unchanged
