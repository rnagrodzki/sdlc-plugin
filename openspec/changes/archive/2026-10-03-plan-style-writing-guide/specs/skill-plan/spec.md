# Spec Delta

## MODIFIED Requirements

### Requirement: Invocation flags
The skill SHALL accept the arguments in its `argument-hint`: `[--auto] [--spec [<change-name>]] [spec-file-path]`.

| Flag | Effect |
|---|---|
| `--auto` | Suppresses the structured-discovery and approach-check questions, the OpenSpec gate-check question (option **Create OpenSpec change** is taken), and every **harden** offer. Choices made without asking go into `## Key Decisions` and get a `## Deviations & assumptions` row with `asked=no`. |
| `--spec` | Opts into OpenSpec and skips the gate-check question. Without a name: use the branch-matched change, else ask which active change to use, or offer **Create OpenSpec change** when none exists. |
| `--spec <change-name>` | Plans directly from `openspec/changes/<change-name>/`; passed to `plan_prepare` as `fromOpenspec` |
| `--from-openspec <change-name>` | Deprecated alias of `--spec <change-name>` for one release; prints `--from-openspec is deprecated — use --spec <change-name>` |
| `[spec-file-path]` | Requirements file; a path into `openspec/changes/<name>/` selects that change |

- `--auto` never bypasses a Gate A `CRITICAL` verdict.
- `--auto` still asks three questions that have no safe default: which OpenSpec change to use when several match, whether to split into N plans, and what to do after `reviewLoop.maxRounds` (5) review rounds with open blocking issues. When AskUserQuestion is unavailable, the skill stops and reports instead of choosing.

#### Scenario: Auto mode at the OpenSpec gate check
- **WHEN** `--auto` is set and the gate check finds a functional request with no matching change
- **THEN** the skill does not call AskUserQuestion
- **AND** it takes **Create OpenSpec change** and adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Auto mode with several OpenSpec changes
- **WHEN** `--auto` is set and several active OpenSpec changes exist with no branch match
- **THEN** the skill asks which change to use with AskUserQuestion

#### Scenario: Auto mode resolves an approach question
- **WHEN** `--auto` is set and decomposition reveals two viable approaches
- **THEN** the skill does not call AskUserQuestion
- **AND** it picks the most conservative option, using the closest match to existing codebase patterns as the tiebreaker, and adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Deprecated alias
- **WHEN** the skill is invoked with `--from-openspec add-widget`
- **THEN** it behaves as `--spec add-widget`
- **AND** it prints `--from-openspec is deprecated — use --spec <change-name>`

### Requirement: Step 3 lane critique
The skill SHALL dispatch all five `lanes[]` entries in one message, using `subagentType`, `model`, and `promptTemplatePath` from `plan_prepare` verbatim and no `isolation` value. It SHALL merge results with `plan_support({action: "merge_results", laneResults, expectedGates: ["G1".."G22"]})`.

- Before the merge, the skill maps each lane's JSON to the tool's lane shape; it never passes the raw lane JSON.

| `laneResults[]` field | Lanes 0–3 | Lane 4 (G17) |
|---|---|---|
| `name` | `lanes[i].name` | `lanes[4].name` |
| `status` | `pass` when `laneStatus` is `ok`; `fail` when `failed`, `timeout`, or no parseable JSON | `pass` when the G17 JSON parsed; `fail` on dispatch failure, timeout, malformed JSON, or null template |
| `gateIds` | the lane's `gateIds` | `["G17"]` |
| `issues[].severity` | `blocking` when the issue has `blocking: true` or `severity: "error"`; otherwise `advisory` | no issues |
| `issues[].summary` | `<taskRef>: <message>`, or `message` when `taskRef` is null | no issues |

- The mapping produces only lane `status` `pass` or `fail` and issue `severity` `blocking` or `advisory`; `merge_results` rejects any other value with a `DomainError` and merges nothing.
- Lane 4 is always in `laneResults`, so G17 is never a coverage gap; a failed lane 4 becomes an advisory note.
- A lane with null `promptTemplatePath` is not dispatched and becomes a synthetic entry with `status: "fail"` and one `blocking` issue.
- Exception: a null `lanes[4]` (G17) template counts as empty advisory findings and is logged with `learnings_log`.
- Step 3 does not edit the plan file.
- The `guardrail-compliance` lane receives `styleGuideFile` and evaluates G22 (style compliance), including the STE rules that need meaning and each custom plan instruction.

#### Scenario: Missing lane template
- **WHEN** `lanes[0].promptTemplatePath` is null
- **THEN** the merged issues include a blocking issue `Lane static-structural skipped — promptTemplatePath null (template not found at prepare time)`

#### Scenario: Lane reports an error-severity issue
- **WHEN** the static-structural lane returns `laneStatus: "ok"` and an issue with `severity: "error"`, `blocking: true`, `taskRef: "Task 3"`, and `message: "Depends on missing Task 9"`
- **THEN** the skill sends that lane with `status: "pass"` and an issue with `severity: "blocking"` and `summary: "Task 3: Depends on missing Task 9"`
- **AND** `merge_results` returns `mergedStatus: Issues Found`

### Requirement: Step 5 review loop
Except for lightweight plans, the skill SHALL review the plan and loop through Step 6 at most `reviewLoop.maxRounds` times, a value that `plan_prepare` returns (5).

| Plan size | Dispatch |
|---|---|
| 5 or more tasks | All 3 `lensReviewers[]` in one message, `run_in_background: false`, model opposite to the plan author's |
| Fewer than 5 tasks | One reviewer from `plan-reviewer-prompt.md` with `{LENS}=all` |

- The skill waits for every dispatched result before merging with `plan_support({action: "merge_results", lensResults})`; the iteration counter moves only then.
- Each lens result is sent as `name` = the lens, `status` = the `**Status:**` value as written (`Approved` or `Issues Found`; the tool ignores letter case), one `blocking` issue per `**Issues**` bullet, and one recommendation per `**Recommendations**` bullet.
- It regenerates `## Verification Scorecard` each round: dimension counts, traceability matrix, and a verdict.
- `Approved` ends the loop; Step 6 is a no-op. `Issues Found` goes to Step 6.
- After round `reviewLoop.maxRounds` with open blocking issues it summarizes them, asks with AskUserQuestion, and offers **harden** unless `--auto` is set. `--auto` does not suppress the question itself.
- The skill contains no hardcoded round number; every limit site reads `reviewLoop.maxRounds`.

#### Scenario: Review does not converge
- **WHEN** the fifth review round still has blocking issues
- **THEN** the skill surfaces the issues with AskUserQuestion instead of looping again

#### Scenario: Third round does not stop the loop
- **WHEN** the third review round still has blocking issues
- **THEN** the skill goes to Step 6 and runs round 4

### Requirement: Handoff
The skill SHALL call `plan_mark({marker: "done"})` and then hand off without running `execute` or `ship` in the same turn.

- With a scorecard, it prints `Verification Scorecard: <verdict line> — see ## Verification Scorecard in the plan for details.`
- With a non-empty `styleReport.instructions`, it prints a self-check table (`# | Instruction | Followed? | Where`) with evidence from the plan file and fixes every "no" row first.
- Plan mode: announce the path with `ship` and `execute` options, then call ExitPlanMode.
- Normal mode: show the menu `ship`, `execute`, `done`; invoke the chosen skill with the Skill tool.
- After writing the plan it appends a learning entry with `learnings_log`.

#### Scenario: Normal mode handoff
- **WHEN** the plan passes both hard gates in normal mode
- **THEN** the skill shows `ship`, `execute`, `done`
- **AND** on `done` it ends without further action

## ADDED Requirements

### Requirement: Writing guide applied
The skill SHALL write every narrative section of the plan per `style.writingGuide` from `plan_prepare`, and SHALL print the style settings and every `style.warnings` entry in Step 0 and on resume.

- The skill does not hardcode writing rules; the guide replaces them.
- The skill marks new and changed Mermaid nodes only with the two classDefs in the guide, in the plan file and in OpenSpec artifacts.
- G22 issues from the `guardrail-compliance` lane are fixed in Step 4 like other blocking issues.

#### Scenario: Warning shown at start
- **WHEN** `style.warnings` has one entry
- **THEN** Step 0 prints that entry under the style settings line

### Requirement: Style report at handoff
Before the instruction self-check and the Step 7 menu, the skill SHALL call `validate({action: "plan_style", file: <plan>})`, adding `template` on the full pipeline, and SHALL print `styleReport` as a table with one row per measured section, then the banned-phrase hits, the STE hits, and the diagram contrast hits.

#### Scenario: Report printed
- **WHEN** Step 6.6 passes
- **THEN** Step 7 prints the style report table above the `ship` / `execute` lines

### Requirement: Contract legend section
The shipped plan template SHALL require `## How to read a task Contract` in place of `## Contract Examples`. Step 2 SHALL write it as one intro sentence and one table with the columns `Key | What it pins | Why the executor needs it | See`, one row per Contract key (`shape`, `names`, `mirror`, `decisions`, `sync`, `example`). Each `See` cell SHALL name a real `Task N` of the same plan, or `not used`. The skill SHALL NOT copy the worked examples from `plan-format-reference.md` into the plan.

- When a project template still lists `Contract Examples`, the skill writes the same legend under that heading.

#### Scenario: Default template
- **WHEN** a plan uses the shipped template
- **THEN** the plan has `## How to read a task Contract` with 6 table rows
- **AND** each `See` cell names a task of that plan or says `not used`

#### Scenario: Old project template
- **WHEN** the active template lists `Contract Examples`
- **THEN** the plan has `## Contract Examples` with the legend table
