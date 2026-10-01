# Spec Delta

## MODIFIED Requirements

### Requirement: decide
The `decide` action SHALL append `{step, decision, at}` to `decisions[]`, with `decision` taken from `detail.text` and `at` set to the call time (RFC 3339 UTC), without checking `step` against `steps[]`.

#### Scenario: Decision for a step with no entry
- **WHEN** the call passes `action:"decide"`, `step:"received-review"`, `detail.text:"fixed 3"`
- **THEN** `decisions[]` ends with `{step:"received-review", decision:"fixed 3", at:<now>}`

### Requirement: cleanup-pipeline
The `cleanup-pipeline` action SHALL settle the current run, delete the plan run linked to this ship run, then run a GC sweep and a per-run-directory reap, unless the contract check fails.

| Path | `currentRun` |
|---|---|
| `detail.force:true` | `{cleaned:false, preservedReason:"force"}`; no check, no stamp |
| No state file | `{valid:true, cleaned:false, reason:"no-state-file"}` |
| Valid contract | `{valid:true, cleaned:true, pipelineStatus:"completed", pipelineCompletedAt}`; file stamped |
| Violation | `DataError` as in `cleanup`; no sweep runs |

| Output field | Meaning |
|---|---|
| `currentRun` | See the table above. |
| `planRun` | `{deleted: true, runId}` when the linked plan run and its `.evidence/` dir were deleted; `{deleted: false, reason}` otherwise (`run not stamped`, `no linked plan run`, `report not written`, `remove failed: <error>`). |
| `gc` | `{ship, execute, plan, commit}`, each `{deleted, kept}`. |
| `directories` | Reap result for stale per-run directories under `.sdlc-v2/runs/`. |
| `force`, `ttlDays` | Resolved inputs. TTL: `detail.ttlDays` > config `state.gc.ttlDays` > `7`. |
| `issueSummary` | `{total, byCategory, items, display, hardenSuggestion?}`; only after a stamp, and only when `issues[]` is non-empty. |

- The linked plan run is the plan run whose `planFilePath` equals the execute state's `planPath`.
- The linked plan run is deleted only after the stamp and only when this run's report file `.sdlc-v2/reports/ship-<runId>-report.<md|json>` exists; otherwise it is left for GC.

| Condition | Class | Message (short) |
|---|---|---|
| `detail.force` not a boolean | `DomainError` | `cleanup-pipeline: detail.force must be a boolean, got <type>` |
| Sweep fails after a stamp | `InfraError` | `run is already marked completed; only the gc sweep over <dir> failed: ...` / call `ship_state gc` to retry the sweep |
| Sweep fails without a stamp | `InfraError` | `gc sweep over <dir>: ...` |

#### Scenario: Force preserves the run
- **WHEN** the call passes `detail.force:true` with an `in_progress` step
- **THEN** `currentRun` is `{cleaned:false, preservedReason:"force"}`
- **AND** the state file is not stamped
- **AND** `planRun` is `{deleted:false, reason:"run not stamped"}` — force never stamps, so the linked plan run is always left for GC

#### Scenario: Issue summary after stamp
- **WHEN** the run stamps and `issues[]` has entries
- **THEN** the response has `issueSummary`

#### Scenario: No issues
- **WHEN** the run stamps and `issues[]` is empty
- **THEN** the response has no `issueSummary`

#### Scenario: Plan run deleted after the report
- **WHEN** the report was written and the run stamps
- **THEN** the linked `plan-<slug>-<ts>.json` and its `.evidence/` dir are deleted
- **AND** `planRun.deleted` is `true`

#### Scenario: Report not written
- **WHEN** the run stamps but no report was written
- **THEN** the linked plan run is kept
- **AND** `planRun` is `{deleted:false, reason:"report not written"}`

### Requirement: report
The `report` action SHALL compose the end-of-run report for the branch's ship run and render it, and SHALL write it to `.sdlc-v2/reports/ship-<runId>-report.<md|json>` only when `detail.write` is `true`.

- `detail.format` (`md` \| `json`) is validated first; the default is config `automation.report.format`, else `md`.
- Config `automation.report.enabled = false` returns `{skipped:true}` before any state read.
- A config read error other than not-found uses the defaults and adds an `issues` warning with `category:"cross-read"`.
- `runId` is the run's `startedAt` with every character except digits and `T` removed (e.g. `20260327T143000`).
- `execution` and `guardrailHits` come from the branch's execute state, only when the `execute` step is `completed`.
- `plan` is the latest `plan` record in `.sdlc-v2/history/runs.jsonl` (last 100) whose plan file equals the execute state's plan path; else `null` with `planNote` `no plan linked to this run`, or `plan history could not be read`.
- `planning` comes from the linked plan run state file (same plan path): `{planFile, decisions[], milestones[]}`. `decisions[]` are its `criticalDecisions` `{key, choice, rejected, reason, at}`; `milestones[]` are its `planIntegrity` timestamps as `{name, at}` in time order. When no plan run file exists, `planning` is `null` with `planningNote` `plan run state not found`.
- `timeline` is one list of `{at, phase, event}` sorted by `at`, merged from: plan milestones and decisions (`phase:"plan"`), execute wave starts, completions, and base syncs (`phase:"execute"`), ship step begins and ends and `decisions[]` (`phase:"ship"`).
- `cliEvidence` is the branch's `.sdlc-v2/evidence/cli-executions.jsonl` entries since the run's `startedAt`.
- It works on a stamped state and never writes the state file.

| Output field | Meaning |
|---|---|
| `branch`, `runId`, `format`, `bump`, `duration` | Run identity and summary. |
| `plan`, `planNote` | Plan timing `{planFile, startedAt, lastModifiedAt, durationMs}` or `null` with a note. |
| `planning`, `planningNote` | Plan decisions with rejected alternatives, and plan milestones; or `null` with a note. |
| `timeline` | Merged plan → execute → ship event list. |
| `steps`, `issues`, `decisions` | Step timings, state issues plus cross-read warnings, decision lines. |
| `reviewLedger`, `reviewLedgerNote`, `healing` | As in `read`'s `reportData`. |
| `deferredFindings` | The state's `deferredFindings[]` entries. |
| `hardenCommit` | The `harden` step's `result` when that step is `completed`. |
| `execution`, `guardrailHits`, `cliEvidence`, `linkedLearnings` | Cross-read data. |
| `display` | `md`: the full Markdown report, emitted raw; it has a `## Planning` section (decision table `Decision \| Chosen \| Rejected \| Reason`) and a `## Timeline` section. `json`: one line `Ship run <runId> on <branch>: <c>/<n> steps completed, <f> findings fixed, <d> deferred, <g> guardrail hits.` |
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

#### Scenario: Planning section
- **WHEN** the linked plan run has a `criticalDecisions` entry `{key:"base-sync-method", choice:"merge", rejected:[{option:"rebase", why:"rewrites SHAs"}]}`
- **THEN** `display` has a `## Planning` row `base-sync-method | merge | rebase: rewrites SHAs | ...`

#### Scenario: Timeline order
- **WHEN** the plan was done at 10:00, wave 1 started at 10:05, and ship `pr` began at 10:30
- **THEN** `timeline` lists those three events in that order with phases `plan`, `execute`, `ship`
