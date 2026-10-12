# Spec Delta

## MODIFIED Requirements

### Requirement: Auto mode
The skill SHALL honour `--auto` only when it appears in its own arguments outside any quoted value, and under `--auto` SHALL call `AskUserQuestion` nowhere while keeping every no-relax and validate-and-revert rule.

- `--auto` is never inferred from pipeline context, conversation history or running as a subagent.
- Text inside `--failure-text "..."` that reads `--auto` is failure text, not a flag.
- `--auto` with `--from-learnings` is invalid: the skill stops with an error before calling `prepare_orchestrator`.
- Under `--auto` the skill never invokes `error-report`.
- Under `--auto` the skill applies only the actions that the requirement "Auto-mode action table and pending items" allows.

#### Scenario: --auto inside failure text
- **WHEN** the skill is invoked with `--failure-text "reviewer said --auto" --skill ship`
- **THEN** it runs with per-proposal approval prompts

#### Scenario: --auto with --from-learnings
- **WHEN** the skill is invoked with `--from-learnings --auto`
- **THEN** it stops with an error
- **AND** it does not call `prepare_orchestrator`

### Requirement: Orchestrator dispatch
The skill SHALL dispatch `sdlc:harden-orchestrator` with model `sonnet` and a prompt of exactly two lines, `MANIFEST_FILE: <manifestPath>` and `PROJECT_ROOT: <repository.contentRoot>`, and SHALL stop when the response is not valid JSON.

The subagent returns one JSON object:

| Field | Meaning |
|---|---|
| `classification` | `user-code`, `plugin-defect` or `ambiguous` |
| `classificationRationale` | One sentence tied to the failure text or a manifest field |
| `routeToErrorReport` | `true` only for `plugin-defect` |
| `errorReportPayload` | `skill`, `step`, `operation`, `errorText`, `exitOrHttpCode`, `errorType`, or `null` |
| `skipped.reviewDimensions.rationale` | Why no review-dimension proposal was made |
| `skipped.drafts[]` | `failureClass` and `reason` of each draft that failed the generic-rule gate |
| `proposals[]` | `surface`, `action` (`add` / `strengthen` / `consolidate` / `repair` / `remove` / `merge`), `targetFile`, `patch`, `rationale`, `failureClass`, `extends`, `evidence`, `oldText`, `removalReason`, `removeIds`, `mergedInto`, `deadReferenceTokens` |

- The subagent only reads files. It does not call `gh` or `git` and writes nothing.
- `proposals` list `repair` proposals first, then `review-dimensions`, `plan-guardrails`, `execute-guardrails`, `copilot-instructions`.

#### Scenario: Orchestrator returns prose
- **WHEN** the subagent response is not parseable JSON
- **THEN** the skill shows the raw response and stops without retrying
- **AND** it removes the manifest file

### Requirement: Classification branching
The skill SHALL pick the next step from the orchestrator result by four ordered conditions, and the first condition that matches SHALL apply: (a) any `repair` proposal goes to the proposal loop; (b) `plugin-defect` with `routeToErrorReport: true` goes to the plugin-defect route with no surface edits; (c) any other proposal goes to the proposal loop after the classification and rationale are shown; (d) else the skill exits.

- After the proposal loop, a `plugin-defect` result goes to the plugin-defect route after the 5e record.

#### Scenario: No proposals
- **WHEN** the classification is `user-code` and `proposals` is empty
- **THEN** the skill prints `No actionable hardening proposals — the failure signal does not point at any of the loaded surfaces.`
- **AND** it removes the manifest and exits

#### Scenario: Plugin defect with a repair
- **WHEN** the classification is `plugin-defect` and `proposals` holds one `repair` proposal
- **THEN** the skill presents the repair first
- **AND** it goes to the plugin-defect route after the 5e record

### Requirement: Review-finding cluster dispatch
Callers that harden from review findings (`received-review` Step 11.6, `ship` step `harden`) SHALL build clusters with `ship_state({action: "harden_clusters"})` and SHALL invoke the skill once per cluster whose `alreadyHardened` is false, one cluster at a time.

- Dispatch shape: `--failure-text "<cluster.failureText>" --skill <caller> --step "<caller step>" --operation "review-feedback-driven hardening"`.
- `failureText` is passed verbatim; `harden_clusters` already replaced `"` with `'` and `\` with `/`.
- `--auto` is added only when the caller's own invocation had `--auto`.
- Unaccounted findings are not passed as input.
- A `below-threshold` deferral is passed with verdict `not-evaluated` and its `line`, `deferredId` and `fixedInRun`.

#### Scenario: Finding text contains a flag
- **WHEN** a cluster `failureText` contains `--auto` and the caller ran without `--auto`
- **THEN** the dispatch does not carry `--auto` as a flag

#### Scenario: Below-threshold finding in input
- **WHEN** the ship state has a `below-threshold` run entry with `deferredId` `review-deferred-x-1`
- **THEN** the caller passes it with verdict `not-evaluated` and `deferredId` `review-deferred-x-1`

### Requirement: Custom instruction block
The skill SHALL print the `customInstructions` lists after the prepare step, SHALL pass them to the orchestrator through the manifest, and SHALL keep every approval gate, no-relax rule and `--auto` rule unchanged.

#### Scenario: Lists configured
- **WHEN** `plan-guardrails` has 1 item
- **THEN** the skill prints `Custom harden instructions (config.toml [harden.instructions] — they shape proposals, never relax a rule):` and the item

#### Scenario: None configured
- **WHEN** all four lists are empty
- **THEN** the skill prints `Custom harden instructions: none configured.`

#### Scenario: Summary line
- **WHEN** the run reaches the Step 5d summary
- **THEN** the summary has the line `Custom instructions: <N> configured (config.toml [harden.instructions])`

#### Scenario: Severity downgrade repair
- **WHEN** validation returns a severity-downgrade finding for a proposal
- **THEN** the skill repairs the proposal once and keeps the old severity
- **AND** a repair that fails validation skips the proposal with `skipped: severity downgrade`

## ADDED Requirements

### Requirement: Pre-flight findings become repair proposals
The skill SHALL continue when `prepare_orchestrator` reports pre-flight findings, SHALL print `preflightFindingCount`, and SHALL present one `repair` proposal for each pre-flight finding before every other proposal.

#### Scenario: Oversize rule at start
- **WHEN** a plan guardrail description is 1474 bytes
- **THEN** the skill prints a `preflightFindingCount` of 1
- **AND** the first proposal has action `repair` and evidence `preflight`

#### Scenario: Config does not parse
- **WHEN** `prepare_orchestrator` returns an InfraError
- **THEN** the skill stops before the orchestrator dispatch

### Requirement: Rule removal and merge
The skill SHALL remove a rule only for a dead reference, a covering rule or a pre-flight repair. Each preview SHALL show the full old text. A guardrail is deleted by a JSON `null` on its id in `setup_write_sections`.

#### Scenario: Dead-reference removal
- **WHEN** the user approves a `remove` proposal with `removalReason` `dead-reference` for guardrail `old-rule`
- **THEN** the skill calls `setup_write_sections` with `{"plan.guardrails.old-rule": null}`
- **AND** the preview showed `oldText` in full

#### Scenario: Merge check before the delete
- **WHEN** a merge of review dimension `a.md` into `b.md` gets a finding from `validate` action `dimensions` with `file` `b.md`
- **THEN** the skill reverts `b.md`
- **AND** the skill deletes no file

#### Scenario: Restore fails
- **WHEN** the check after a delete has a finding
- **AND** the write-back of `oldText` fails
- **THEN** the skill stops the proposal loop
- **AND** it prints `oldText` in full

#### Scenario: Mirror follows a removal
- **WHEN** review dimension `x` is removed
- **AND** `.github/instructions/x.instructions.md` exists
- **THEN** the skill deletes that mirror file

### Requirement: Scoped checks in the write step
The skill SHALL check each guardrail proposal in memory with `validate` `candidatesJson` and SHALL check the written ids on disk with `scopeIds`, so an older finding in the section does not fail a new proposal.

#### Scenario: Old finding does not block a new rule
- **WHEN** the section holds an old oversize rule
- **AND** a proposal adds the clean rule `new-rule`
- **THEN** the in-memory check returns no finding
- **AND** the on-disk check passes `scopeIds` `["new-rule"]`

### Requirement: Auto-mode action table and pending items
Under `--auto` the skill SHALL apply add, strengthen, consolidate, pre-flight repairs, and dead-reference repairs and removals whose tokens are all in `surfaces.deadReferences`. It SHALL store each other proposal in one deferred item for each rule section.

- The pending call is `ship_state` `deferred_add` with id `harden-pending-<section>`, source `harden-pending` and `merge:true`.

#### Scenario: Merge waits for a person
- **WHEN** `--auto` is set and a proposal has action `merge` on `plan-guardrails`
- **THEN** the skill writes no rule for it
- **AND** it calls `deferred_add` with id `harden-pending-plan`
- **AND** the 5d summary lists it under `Pending (needs a person`

#### Scenario: Unproven dead reference
- **WHEN** `--auto` is set and a `remove` proposal names a token that is not in `surfaces.deadReferences`
- **THEN** the skill stores the proposal as pending

#### Scenario: Pending write fails
- **WHEN** `deferred_add` returns an error
- **THEN** the 5d summary prints the items under `Pending (NOT stored — copy them by hand)`
- **AND** the run continues to the 5e record

### Requirement: Generic-rule gate
The harden orchestrator SHALL state a `failureClass` for each draft. A draft that names one case only SHALL be dropped with one `skipped.drafts` entry. A draft whose class an existing rule covers SHALL become `strengthen` or `consolidate` with `extends`.

#### Scenario: One-case draft
- **WHEN** a draft names one file as its only condition and no class is found
- **THEN** `proposals` does not hold it
- **AND** `skipped.drafts` holds one entry with its reason

#### Scenario: Covered class
- **WHEN** a draft matches the class of guardrail `dry`
- **THEN** the proposal has action `strengthen` or `consolidate` and `extends` `dry`

### Requirement: Learnings and pending items as proposal input
A learnings pattern with `patternCount` 3 or more and no covering rule SHALL become a proposal with evidence `learning`. For failure text `Pending rule edits: <id>`, the orchestrator SHALL draft again each edit of open deferred item `<id>`.

#### Scenario: Recurring learning
- **WHEN** the manifest has a learnings pattern with `patternCount` 3 and no rule covers it
- **THEN** one proposal has evidence `learning`

#### Scenario: Pending item run
- **WHEN** the failure text is `Pending rule edits: harden-pending-plan`
- **THEN** the proposals repeat the edits listed in that open deferred item
