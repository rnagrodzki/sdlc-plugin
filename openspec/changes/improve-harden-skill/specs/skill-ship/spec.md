# Spec Delta

## MODIFIED Requirements

### Requirement: Per-finding review routing
After `review` completes, the skill SHALL route each `#### [<SEVERITY>] <title>` finding on its own severity against `flags.reviewThreshold`, and SHALL NOT route on the `Verdict:` line.

- First, record `ship_state({action:"healing_record", step:"review", detail:{kind:"review-total", total:<M>, dimensions:<N>}})` from the `## Code Review — {N} dimension(s), {M} finding(s)` header; skip it when `M` is unreadable.
- At or above the threshold: collect the finding for `received-review`.
- Below the threshold: `ship_state({action:"defer", step:"review", detail:{severity:<lowercase>, file, title, line, reason:"below-threshold", description:<finding body>}})`, one call per finding.
- When the `defer` summary has `WARNING: could not persist this item`: call `deferred_add` once with the named `detail.id`, `description:"<file>:<line> [<severity>] <title> — reason: below-threshold"`, `source:"review-below-threshold"`, and `priority` high/medium/low from severity; never retry `defer`. If that fails too, name the finding UNACCOUNTED.
- Always record `ship_state({action:"decide", step:"review", detail:{text:"bypassed N finding(s): ..."}})`, even for zero.

| Parsed headings vs `M` | Action |
|---|---|
| `M` is 0 | Finding-free; record `bypassed 0 finding(s)`. |
| Parsed equals `M` | Route as above. |
| Parsed fewer than `M` | Put `parsed X of M heading(s)` in the `decide` text; ask the user whether to re-run `/review` first. |
| `M` unreadable | No `review-total` record; the summary prints `reviewLedgerNote`. |

#### Scenario: Medium finding under a high threshold
- **WHEN** `flags.reviewThreshold` is `high` and review reports `#### [MEDIUM] Extract helper`
- **THEN** the skill calls `defer` with `severity:"medium"`, `reason:"below-threshold"` and the finding body as `description`

#### Scenario: Heading shortfall
- **WHEN** review's header says 14 findings and 9 headings parse
- **THEN** the `decide` text contains `parsed 9 of 14 heading(s)`
- **AND** the skill never records `bypassed 0`

### Requirement: harden step
When `harden` is in `flags.steps`, the skill SHALL turn review findings into clusters with `ship_state({action:"harden_clusters"})`, invoke the `harden` skill once per approved cluster with the `Skill` tool, commit the edited surfaces as a separate commit, and then run one fix pass for the `fixNow` findings with its own commit.

- Input findings come from a fresh `read`: `reportData.healing.fixed` records without a `deferredId` (`body:""`, `verdict:"agree-will-fix"`) and the top-level `deferredFindings` array (`body` from `description`, plus `line`, `deferredId`, `fixedInRun`; verdict `not-evaluated` for reason `below-threshold`), in stored order; never from review text in the conversation.
- Invocation: `Skill("harden", "--failure-text \"<failureText>\" --skill ship --step \"ship harden\" --operation \"review-feedback-driven hardening\" [--auto]")`; `failureText` is copied verbatim; `--auto` only from `flags.auto`.
- Consent: under `flags.auto`, every cluster with `alreadyHardened:false` runs; otherwise `AskUserQuestion` per cluster `dispatch | skip`, each skip recorded with `decide`.
- A failed invocation is recorded with `decide` (`cluster <key>: harden failed — <error>`) and the next cluster runs.
- After the clusters, `harden_clusters` runs again with the same input; non-empty `dirtySurfaces` → stage those paths, dispatch `commit` (haiku, `--auto`). Then the fix pass runs when `fixNow` is not empty, else `complete-step` (`"<sha> <subject>"` or `"harden made no changes"`).
- Fix pass: a tree that is not clean outside `.sdlc-v2/` and `openspec/` skips it with `decide`. Else dispatch `received-review` (opus, `--no-harden`, `--auto` under `flags.auto`) with each `fixNow` finding and its `**Deferred-id:**` line.
- Fix-pass commit: stage the exact paths from `git status --porcelain -z -- . ':!.sdlc-v2/' ':!openspec/'` with `git add -- <paths>`, dispatch `commit` (haiku, `--auto`), record `decide` `fix-pass commit <sha>`.
- A fix-pass dispatch failure is not fatal: the findings stay deferred and the step completes with a note. A staging or commit failure calls `fail` with step `harden`.

| Condition (checked in this order) | Action |
|---|---|
| No review ran, or review's `M` is 0 | `skip` with reason `"no review findings"` |
| Resumed, `decisions[]` has `fix-pass commit <sha>` and `<sha>` is an ancestor of `HEAD` | `complete-step` with that sha |
| Resumed, tree not clean outside `.sdlc-v2/` and `openspec/`, no cluster with `alreadyHardened:false` | Interactive: ask to commit the paths as the fix-pass commit or stop. `flags.auto` or stop: `fail` |
| 0 clusters in total, `fixNow` empty | `skip` with reason `"no qualifying clusters"` |
| 0 clusters in total, `fixNow` not empty | Run the fix pass |
| `dirtySurfaces` non-empty, not resumed | `skip` with reason `"harden surfaces dirty before step"` |
| `dirtySurfaces` non-empty, resumed | Continue with the remaining clusters |
| `dirtySurfaces` empty, resumed, no cluster with `alreadyHardened:false` | Run the fix pass when `fixNow` is not empty, else `complete-step` with the last commit on the harden surfaces since the run start, or `"harden made no changes"` |
| `dirtySurfaces` empty, at least one cluster with `alreadyHardened:false` | Continue to consent |

- "Resumed" means `reportData.healing.hardened` is non-empty.
- `suppressed[]` and `loneDisagree[]` are listed in the final summary.

#### Scenario: Dirty surfaces before the step
- **WHEN** `.sdlc-v2/config.toml` has uncommitted edits and no harden record exists
- **THEN** the skill calls `skip` with reason `"harden surfaces dirty before step"`

#### Scenario: Auto hardening
- **WHEN** `flags.auto` is `true` and two clusters have `alreadyHardened:false`
- **THEN** the skill invokes `harden` twice with `--auto`, one at a time

#### Scenario: Fix pass after harden
- **WHEN** the harden commit is done and `fixNow` has 2 findings
- **THEN** the skill dispatches `received-review` once with 2 findings, each with a `**Deferred-id:**` line
- **AND** the fix-pass commit is a separate commit

#### Scenario: Fix pass with a dirty tree
- **WHEN** `src/a.go` has uncommitted edits before the fix pass
- **THEN** the skill records `decide` `fix pass skipped — tree not clean outside .sdlc-v2/ and openspec/`
- **AND** it calls `complete-step`
