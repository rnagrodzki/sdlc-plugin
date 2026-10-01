# Spec Delta

## MODIFIED Requirements

### Requirement: OpenSpec steps
The skill SHALL run `verify-openspec` and `archive-openspec` when `flags.openspecChange` is set (from `--openspec-change` or from the plan's `**Source:**` header) or an active change is auto-detected, and SHALL `skip` them with reason `"no active OpenSpec change"` only when no change is named anywhere.

- When `flags.openspecChange` is set but `openspec/changes/<name>/` and `openspec/changes/archive/*-<name>/` both do not exist, the step fails with `OpenSpec change "<name>" named by the plan is missing on disk` and the pipeline stops.
- `verify-openspec`: `openspec validate "$CHANGE" --strict`; a non-zero exit stops the pipeline.
- `archive-openspec`: when the change is already archived, `skip`; else count `- [ ]` and `- [x]` in `tasks.md`, ask consent (`AskUserQuestion`, skipped under `flags.auto`), mark remaining boxes `[x]` when archiving anyway, re-run `openspec validate --strict`, then `openspec archive "$CHANGE"`.
- A `decide` entry records the archive consent outcome.

#### Scenario: No active change
- **WHEN** `verify-openspec` is configured, no change is named by flag or plan, and none is auto-detected
- **THEN** the skill calls `skip` with reason `"no active OpenSpec change"`

#### Scenario: Named change missing
- **WHEN** the plan's `**Source:**` names `add-widget` and `openspec/changes/add-widget/` does not exist
- **THEN** `verify-openspec` fails with `OpenSpec change "add-widget" named by the plan is missing on disk`
- **AND** the pipeline stops

#### Scenario: Validation fails
- **WHEN** `openspec validate "$CHANGE" --strict` exits non-zero
- **THEN** the pipeline stops

### Requirement: Rebase
After `commit-fixes` and before the next configured step among `harden`, `verify-openspec`, `archive-openspec`, `pr`, the skill SHALL run `git fetch origin <base>` and skip the rebase when `git merge-base --is-ancestor origin/<base> HEAD` succeeds, where `<base>` is the resolved base branch.

- Otherwise `flags.rebase` `"auto"` rebases and `"skip"` never rebases; any other value is informational.
- A conflict stops the pipeline; the user resolves it and runs `--resume`.
- The outcome is always recorded with `ship_state({action:"decide", step:"rebase", detail:{text}})`.

#### Scenario: Already up to date
- **WHEN** `origin/<base>` is already an ancestor of `HEAD`
- **THEN** no rebase runs and a `rebase` decision is recorded

#### Scenario: Conflict
- **WHEN** the rebase hits a conflict
- **THEN** the skill stops and tells the user to resolve it and `--resume`

#### Scenario: Base branch configured
- **WHEN** `[git] baseBranch = "develop"`
- **THEN** the skill runs `git fetch origin develop` and rebases onto `origin/develop` when behind

### Requirement: Terminal cleanup and summary
After the loop, the skill SHALL call `ship_state({action:"report", detail:{write:true}})`, then `ship_state({action:"cleanup-pipeline", detail:{force:false, ttlDays?}})`, then a final `read`, and print the run summary from that read.

- The report is written before cleanup, so the linked plan run still exists when the report reads it; cleanup then deletes the plan run.
- `report` returning `skipped:true` is silent; else the skill prints `display` when `format` is `md` and prints `path` as the last line. A report error is one warning; the run continues.
- A `DataError` from cleanup means a contract violation: show the violating steps and stop; never retry with `force:true`.
- The summary prints step/status/result, `reportData.decisions`, `reportData.deferredFindings`, and the cleanup outcome.
- `issueSummary.display` is printed verbatim, then `issueSummary.hardenSuggestion` when present; nothing is printed when `issueSummary` is absent.
- When `review` ran, the ledger is printed from `reportData.reviewLedger`, never computed by hand:

| `reviewLedger` | Printed |
|---|---|
| `null` | `reviewLedgerNote` verbatim |
| `unaccounted` is 0 | `Review ledger: <total> = <fixed> fixed + <deferredFindings> deferred (<reason>: <n>, ...)` |
| `unaccounted` non-zero | The same line, then `UNACCOUNTED: <unaccounted> finding(s) have no fix or deferral record` |

- When `reportData.deferredFindings` is non-zero: `Run /sdlc:deferred to act on the <n> deferred finding(s).`

#### Scenario: Contract violation
- **WHEN** `cleanup-pipeline` returns a `DataError` for `harden=pending`
- **THEN** the skill shows the violation and stops without `force:true`

#### Scenario: Unaccounted findings
- **WHEN** `reviewLedger.unaccounted` is 2
- **THEN** the summary prints `UNACCOUNTED: 2 finding(s) have no fix or deferral record`

#### Scenario: Report before cleanup
- **WHEN** the loop finishes
- **THEN** `report` with `detail.write:true` is called before `cleanup-pipeline`

### Requirement: End-of-run records
After the summary, the skill SHALL call, in order, `deferred_propose_followups` and `history_record`.

| Call | Behavior |
|---|---|
| `ship_state({action:"deferred_propose_followups"})` | `openCount` 0 → print nothing. Else print `display`; under `--auto` stop there; otherwise run the `/sdlc:deferred` triage flow (file GitHub issues labeled `deferred-followup` then `deferred_resolve`, resolve without filing, or skip). |
| `ship_state({action:"history_record", detail:{skill:"ship", branch, outcome, duration_ms, steps, version?, guardrail_hits?}})` | `outcome` is `success`, `failure`, or `partial`. `version` is the bump sent to `pr`, only when `pr` ran. `guardrail_hits` is the earlier `report` call's `guardrailHits`, only when non-empty. An error is one warning. |

- Every issue body is shown in full and approved in chat before `gh issue create`, with no AI attribution line.
- Execute-drift items already in the store are never re-added with `deferred_add`.
- The skill never renders or writes the report itself.

#### Scenario: No open deferred items
- **WHEN** `deferred_propose_followups` returns `openCount:0`
- **THEN** the skill prints nothing for it

#### Scenario: Report disabled
- **WHEN** the earlier `report` call returned `skipped:true`
- **THEN** `history_record` is called without `guardrail_hits`
