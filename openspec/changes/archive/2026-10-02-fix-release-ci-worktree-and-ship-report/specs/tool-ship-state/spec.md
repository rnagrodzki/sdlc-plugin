# Spec Delta

## MODIFIED Requirements

### Requirement: report
The `report` action SHALL compose the end-of-run report for the branch's ship run and render it, and SHALL write it to `.sdlc-v2/reports/ship-<runId>-report.<md|json>` only when `detail.write` is `true`.

- `detail.format` (`md` \| `json`) is validated first; the default is config `automation.report.format`, else `md`.
- Config `automation.report.enabled = false` returns `{skipped:true}` before any state read.
- A config read error other than not-found uses the defaults and adds an `issues` warning with `category:"cross-read"`.
- `runId` is the run's `startedAt` with every character except digits and `T` removed (e.g. `20260327T143000`).
- `execution` and `guardrailHits` come from the branch's execute state, only when the `execute` step is `completed`.
- `plan` is the latest `plan` record in `.sdlc-v2/history/runs.jsonl` (last 100) whose plan file equals the execute state's plan path; else `null` with `planNote` `no plan linked to this run`, or `plan history could not be read`.
- `planning` comes from the linked plan run state file (same plan path): `{planFile, decisions[], milestones[]}`. `decisions[]` are its `criticalDecisions` `{key, choice, rejected, reason, at}`; `milestones[]` are its `planIntegrity` timestamps as `{name, at}` in time order. When no plan run file exists, `planning` is `null` with `planningNote` `plan run state not found`.
- `timeline` is one list of `{at, phase, event}` sorted by `at`, merged from: plan milestones and decisions (`phase:"plan"`), execute wave starts, completions, and base syncs (`phase:"execute"`), ship step begins and ends and `decisions[]` (`phase:"ship"`). A plan or ship decision whose text is blank (empty or whitespace only) is not turned into an event.
- `cliEvidence` is the branch's `.sdlc-v2/evidence/cli-executions.jsonl` entries since the run's `startedAt`.
- `userInputs` is the branch's `.sdlc-v2/evidence/user-inputs.jsonl` entries since the run's `startedAt`, oldest first, at most the latest 100; an empty list, never `null`, when there are none. A read failure adds an `issues` warning with `category:"cross-read"`.
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
| `userInputs` | Prompts the user typed while the run was active: `{ts, pipeline, step?, wave?, branch, text}`. |
| `display` | `md`: the Markdown report described in "report Markdown layout", emitted raw. `json`: one line `Ship run <runId> on <branch>: <c>/<n> steps completed, <f> findings fixed, <d> deferred, <g> guardrail hits.` |
| `path`, `written` | Report file path and `true` after a write. |
| `skipped` | `true` only when reports are disabled. |
| `next` | `Report persisted. Show the path to the user; do not write it yourself.` after a write; else `Show display to the user. Pass detail.write:true to persist the report.` The `next` text is never part of `display`. |

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
- **AND** the file has no `## Next` section

#### Scenario: Stale execute state excluded
- **WHEN** an execute state exists for the branch but this run's `execute` step is not `completed`
- **THEN** the report has no `execution`

#### Scenario: Planning section
- **WHEN** the linked plan run has a `criticalDecisions` entry `{key:"base-sync-method", choice:"merge", rejected:[{option:"rebase", why:"rewrites SHAs"}]}`
- **THEN** the `## Plan` section of `display` has a row `base-sync-method | merge | rebase: rewrites SHAs | ...`
- **AND** `display` has no `## Planning` heading

#### Scenario: Timeline order
- **WHEN** the plan was done at 10:00, wave 1 started at 10:05, and ship `pr` began at 10:30
- **THEN** `timeline` lists those three events in that order with phases `plan`, `execute`, `ship`

#### Scenario: Blank decision not in the timeline
- **WHEN** a ship `decisions[]` entry for step `await-remote-review` has an empty decision text
- **THEN** `timeline` has no event for it

## ADDED Requirements

### Requirement: report Markdown layout
The `report` action SHALL render `md` `display` with the sections below, in this order, and SHALL summarize high-volume data as counts instead of one line per record.

| Section | Content |
|---|---|
| Title | `# Ship run report — <branch>` |
| `## Summary` | Table `Area \| Result` with rows Run, Plan, Steps, User input, Execution, Review, Fixed by severity, Hardened, Deferred, Guardrail hits, CLI commands, Decisions, Learnings (see below) |
| `## Plan` | Plan file and planning time (or the existing "not available" line), then the critical-decision table `Decision \| Chosen \| Rejected \| Reason` with short-form cells (120 characters), or the existing "no planning data" / "no critical decisions" line. No milestone lines; milestones appear in `## Timeline` |
| `## Steps` | As before |
| `## User input` | `<n> prompts typed during the run.` (plus `Shows the latest 100 prompts only.` when 100 were read), then table `At \| Step \| Text`, oldest first; Step is the ship step, `wave <n>` for an execute entry, or `—`; Text uses the short form |
| `## Timeline` | Table `At \| Phase \| Event`; event text uses the short form (below) |
| `## Review ledger` | Total, fixed, deferred by reason, unaccounted; a negative unaccounted adds `— ledger mismatch: fixes plus deferrals exceed the review total` |
| `## Self-healing` / `### Fixed` | `<n> findings fixed.`, table `Severity \| <one column per origin> \| Total` (severities critical, high, medium, low, info, unknown; rows with 0 omitted), then `Critical, high and medium:` list `- [<sev>] <file>:<line> — <title> (<origin>)` in that severity order |
| `### Hardened` | `<r> runs, <a> edits applied, <s> skipped.`, table `Trigger \| Class \| Applied \| Skipped` (one row per run, short trigger), table `Surface \| Edits \| Files` (files as paths from their first `.sdlc-v2/` or `.github/` segment, de-duplicated) |
| `### Harden commit` | As before |
| `## Deferred` | One line per finding, as before |
| `## Execution` | Tasks line, duration, table `Wave \| Status \| Tasks \| Duration \| Commit` (7-char SHA, `—` when absent), issues line |
| `## Guardrail hits` | As before |
| `## CLI evidence` | `<n> commands, <f> failed.`, table `Step \| Commands \| Failed` (steps in first-seen order; step falls back to pipeline), table `Command \| Runs \| Failed` (commands grouped by program name, plus the subcommand for `git`, `gh`, `go`, `task`, `npm`, `pnpm`, `openspec`; most runs first; at most 15 rows, the rest in one `other (<k> kinds)` row), `Failed commands:` list `- <code span> — exit <code> (<step>)` capped at 20 with `- … <k> more`, and `Full log: .sdlc-v2/evidence/cli-executions.jsonl`; when 200 entries were read, the count line adds `Counts cover the latest 200 commands only (evidence read limit).` |
| `## Decisions` | `- <step>: <short decision>`; entries whose decision text is blank are dropped |
| `## Learnings` | As before |

- Summary rows use the form `label value` joined by ` · ` (for example `total 12 · fixed 11 · deferred 1 · unaccounted 0`). A row whose source is missing shows `—`; the Execution row shows `not run` when `execution` is absent.
- Every Summary number equals the matching number in the section below it.
- Sanitizing: no stored string reaches `display` raw. A command is rendered as one inline code span of its short form, fenced with one backtick more than the longest backtick run inside it. Free text in a list line goes through the short form; a table cell goes through the short form and has `|` escaped.
- Short form: first non-blank line, runs of whitespace collapsed to one space, cut to 200 characters (120 for a command, trigger or plan decision cell) with `…` appended when cut or when later lines were dropped.
- Every empty section still renders one explicit `_No ..._` line: `_No CLI evidence recorded._`, `_No failed commands._`, `_No findings fixed._`, `_No harden runs recorded._`, `_No waves recorded._`, `_No decisions recorded._`, `_No user input during the run._`.
- A finding with an empty severity counts under `unknown`.

#### Scenario: Summary matches the sections
- **WHEN** the run has 11 fixed findings (5 high, 1 medium, 5 low), 1 deferred finding, and 200 evidence entries with 0 failures
- **THEN** the `## Summary` table has `Fixed by severity | critical 0 · high 5 · medium 1 · low 5`, `Deferred | 1`, and `CLI commands | total 200 · failed 0 · latest 200 only`
- **AND** `### Fixed` says `11 findings fixed.`

#### Scenario: Execution not run
- **WHEN** the report has no `execution`
- **THEN** the Summary row is `Execution | not run`

#### Scenario: Medium findings listed
- **WHEN** fixed findings include one `medium` and one `low`
- **THEN** the `Critical, high and medium:` list has the medium finding
- **AND** the low finding appears only in the count table

#### Scenario: CLI evidence becomes counts
- **WHEN** the run has 150 evidence entries, 2 with a non-zero exit code, across steps `ship`, `review`, `pr`
- **THEN** `## CLI evidence` has a 3-row `Step | Commands | Failed` table and exactly 2 `Failed commands:` lines
- **AND** no successful command text appears in `display`

#### Scenario: Evidence read limit reached
- **WHEN** the run has 200 evidence entries
- **THEN** the `## CLI evidence` count line contains `Counts cover the latest 200 commands only`

#### Scenario: Multi-line decision
- **WHEN** a `decisions[]` entry for step `commit-fixes` has a 900-character, 3-paragraph text
- **THEN** `## Decisions` has one line for it of at most 200 characters after `- commit-fixes: `, ending in `…`

#### Scenario: Blank decision dropped
- **WHEN** a `decisions[]` entry for step `await-remote-review` has an empty decision text
- **THEN** no `- await-remote-review:` line appears in `## Decisions`

#### Scenario: Multi-finding harden trigger
- **WHEN** a hardened record's `trigger` holds two findings separated by blank lines
- **THEN** its `### Hardened` table row shows only the first line, at most 120 characters

#### Scenario: Negative ledger gap
- **WHEN** `reviewLedger.unaccounted` is `-1`
- **THEN** `display` has `- Unaccounted: -1 — ledger mismatch: fixes plus deferrals exceed the review total`

#### Scenario: Multi-line failed command
- **WHEN** a failed evidence entry's command is a 50-line heredoc that contains a backtick pair
- **THEN** its `Failed commands:` entry is exactly one line holding the first command line followed by `…`, inside one code span
- **AND** no other line of the heredoc appears in `display`

#### Scenario: Commands grouped by name
- **WHEN** the run has 73 `grep` commands, 10 `git diff` commands and 10 `git -C /repo log` commands
- **THEN** the `Command | Runs | Failed` table has rows `grep | 73`, `git diff | 10` and `git log | 10`

#### Scenario: User prompts listed
- **WHEN** the evidence file holds, for this branch and after the run's `startedAt`, a prompt at step `review` and a 3-line prompt holding `|` at step `pr`
- **THEN** `## User input` says `2 prompts typed during the run.` and has 2 table rows in time order
- **AND** the `pr` row shows only the first line of the prompt followed by `…`, with `|` escaped
- **AND** the Summary row is `User input | prompts 2`

#### Scenario: No user input
- **WHEN** no prompt was recorded during the run
- **THEN** `## User input` shows `_No user input during the run._`
- **AND** the Summary row is `User input | none`
- **AND** `userInputs` is an empty list
