# Spec Delta

## MODIFIED Requirements

### Requirement: report action
The `report` action SHALL assemble the end-of-run execution report without writing anything unless `write` is true, and SHALL return `{skipped: true, written: false}` when config `automation.report.enabled` is false.

| Output field | Meaning |
|---|---|
| `branch`, `runId`, `planPath`, `startedAt`, `duration` | run metadata; `runId` derived from `startedAt`; `duration` from start to the run end: `runCompletedAt` when set, else the latest wave `completedAt`, else now |
| `format` | `format` input, else config `automation.report.format`, else `md` |
| `waves[]` | `{number, status, startedAt, completedAt, duration, tasks[], committedSha}` |
| `waves[].tasks[]` | `{id, name, status, complexity, risk, filesChanged}`; `filesChanged` comma-joined |
| `totalTasks`, `completedTasks`, `failedTasks`, `skippedTasks` | `totalTasks` from state; counts from row statuses |
| `drifts`, `errors`, `warnings`, `concerns` | issues bucketed as below |
| `pendingIssueDrafts` | the state's drafts, when any |
| `deferredFindings` | ship state `deferredFindings`, when any |
| `decisions` | `context.decisionsFromPriorWaves` |
| `cliEvidence` | this branch's evidence lines since the ship start (else the run start), at most 200 |
| `stepTimings` | ship state steps `{name, status, startedAt, duration, humanWait}` |
| `guardrailHits` | ids from `guardrailDecisions` with `decideType` `guardrail` |
| `linkedLearnings` | count of lines in `.sdlc-v2/learnings/log.md` containing `sdlc:run=<runId> ` |
| `path`, `written`, `next` | write results; `next` is empty on a read-only call |

- Buckets: category `drift` → `drifts`; category `done-with-concerns` → `concerns`; other severity `error` → `errors`; other severity `warning` → `warnings`; anything else is dropped.
- A failed evidence or learnings read adds a `{severity: "warning", category: "cross-read"}` item to `warnings` instead of failing.
- `cliEvidence`, `stepTimings`, and `guardrailHits` are empty lists, never `null`, when there is no data.

#### Scenario: Reporting disabled
- **WHEN** `automation.report.enabled` is false
- **THEN** the result is `{skipped: true, written: false}` before any state lookup, even when `write` is true

#### Scenario: Default read-only report
- **WHEN** `report` is called without `write`
- **THEN** the full report is returned with `written: false`
- **AND** no file and no state is written

#### Scenario: No ship state
- **WHEN** the run was not dispatched by ship
- **THEN** `stepTimings` is empty and `deferredFindings` is omitted

#### Scenario: Learnings prefix collision
- **WHEN** the log has lines tagged `sdlc:run=<runId>1`
- **THEN** those lines are not counted for `<runId>`

#### Scenario: Duration ends at run completion
- **WHEN** the run started at 10:00, its last wave completed at 11:00, `runCompletedAt` is unset, and the report is built at 12:00
- **THEN** `duration` is `1h 00m`
