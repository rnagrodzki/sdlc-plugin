# /harden

Analyze a pipeline failure and propose user-approved edits to the project's
hardening surfaces — plan/execute guardrails, review dimensions, or Copilot
instructions — so the same class of failure is caught earlier next time.
Harden adds, tightens and repairs rules. It never relaxes a rule. It removes or
merges a rule only for a dead reference or a covering rule, and only after
approval. The one exception is `--auto`: see
[What harden can remove](#what-harden-can-remove).

## When to use

- A pipeline step (`/plan`, `/execute`, `/review`, ...) just failed and you
  want to prevent the same failure from recurring.
- You want to turn a one-off mistake into a durable guardrail or review
  dimension instead of just fixing the immediate symptom.
- Another skill's failure-handling menu offered a "harden" option and you
  picked it.

## Syntax

    /harden --failure-text <text> --skill <name> [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--failure-text <string>` | The failure to analyze, as free text. Required unless `--from-issue` is used; mutually exclusive with it. | — |
| `--from-issue <num>` | Analyze a GitHub issue instead of inline text. Mutually exclusive with `--failure-text`. | — |
| `--skill <name>` | Name of the skill that failed. Always required. | — |
| `--step <s>` | Step name within the failing skill, if known. | — |
| `--operation <op>` | Operation being performed when the failure occurred. | — |
| `--exit-code <code>` | Exit code or HTTP status from the failure, if any. | — |
| `--error-type <type>` | Category of error, if known. | — |
| `--user-intent <text>` | What the user was trying to accomplish. | — |
| `--args-string <text>` | The invocation arguments that led to the failure. | — |
| `--auto` | Run without prompts, and list each applied proposal under "Auto-accepted" in the output. Two callers pass it: [`/received-review --auto`](received-review.md), when its Step 11.6 dispatches `/harden` (subagent dispatch, where `AskUserQuestion` is not available), and [`/ship --auto`](ship.md)'s own `harden` step, which forwards the flag because the run is unattended. Applies add, strengthen, consolidate, repair and dead-reference removal. Merges and covered-by removals wait as one deferred item for each rule section (see [Auto mode and pending edits](#auto-mode-and-pending-edits)). Each edit is still validated, and `/harden` reverts it on failure. Custom instructions still apply (see [Custom instructions](#custom-instructions)). Never files an `error-report` issue. Not valid with `--from-learnings`. | off |

## Custom instructions

`/harden` can follow project guidance that you write in the
`[harden.instructions]` table of `.sdlc-v2/config.toml`. Each list holds
plain-text instructions for one proposal surface. The orchestrator reads them
from the manifest when it drafts proposals. Edit this table by hand.
[`/setup`](setup.md#config-templates) does not ask about it.

```toml
[harden.instructions]
plan-guardrails = ["Prefer error severity for CI rules."]
```

| Key | Proposals it shapes | Default | Limit |
|-----|---------------------|---------|-------|
| `plan-guardrails` | Plan guardrails | `[]` | 10 items, 1024 characters each |
| `execute-guardrails` | Execute guardrails | `[]` | 10 items, 1024 characters each |
| `review-dimensions` | Review dimensions | `[]` | 10 items, 1024 characters each |
| `copilot-instructions` | Copilot instructions | `[]` | 10 items, 1024 characters each |

The four lists are independent. A missing table, a missing key and an empty
list all mean "no instructions". The limits apply to each list. `/harden`
counts the characters of an item before it trims the item, and it ignores an
item that is blank after the trim. The config schema requires at least 1
character in each item.

**To change the instructions:** edit the lists in `.sdlc-v2/config.toml`, save
the file, then run `/harden` again. `/harden` reads the table at the start of
each run. The config template at `plugins/sdlc/templates/config.toml` shows
the same four keys.

**What `/harden` prints:** after it loads the failure, `/harden` prints each
list, one line for each surface, or `Custom harden instructions: none
configured.` when all four lists are empty. In `--from-learnings` mode it
prints the block once, after the first entry that loads.

**Rules that custom instructions never change:**

- No relaxing. An instruction cannot make `/harden` relax a rule, or remove a
  rule outside the two removal cases.
- Approval prompts. An instruction cannot add or skip an approval prompt.
- `--auto`. An instruction cannot turn `--auto` on or off. In an `--auto` run
  the instructions still apply, and the summary shows
  `Custom instructions: <N> configured (config.toml [harden.instructions])`,
  where `<N>` is the total number of items in the four lists.
- Target files. Each proposal is still limited to the files of its surface.

**An invalid table stops the run.** An unknown key, a value that is not a list,
more than 10 items, an item that is not a string and an item over 1024
characters each give an error that names the key and a suggested fix. `/harden`
does not offer `error-report` for this error, because the cause is the config
and not the plugin. Fix the file and run `/harden` again. A `config.toml` that
`/harden` cannot read or parse also stops the run. In `--from-learnings` mode
the first such error stops the whole triage run, and `/harden` removes no
learnings entry.

## Examples

**Analyze a failure from `/plan`:**

    /harden --failure-text "Step 5 reviewer loop exceeded max retries without converging" --skill plan --step "Step 5" --operation "reviewer-loop"

Loads the failure context, classifies it as user-code, plugin-defect, or
ambiguous, and — for user-code or ambiguous failures — proposes guardrail or
review-dimension edits you can apply, skip, or cancel one at a time.

**Analyze a failure already filed as a GitHub issue:**

    /harden --from-issue 42 --skill execute

Fetches the issue body and (if labeled `mcp-failure`) treats it as a
pre-classified plugin defect, skipping straight to the `error-report` route.

**Replay the rule edits that an `--auto` run left pending:**

    /sdlc:harden --failure-text "Pending rule edits: harden-pending-plan" --skill deferred

Drafts again each edit that the deferred item `harden-pending-plan` lists. Each
edit goes through the normal approval prompt. [`/deferred`](deferred.md) prints
this command for you. See
[Auto mode and pending edits](#auto-mode-and-pending-edits).

## Pre-flight findings

Before it drafts proposals, `/harden` checks the guardrails and review
dimensions that already exist. A finding does not stop the run. `/harden`
prints one line, and it drafts one repair proposal for each finding:

    Pre-flight findings: <N> (repair proposals come first)

The repair proposals come first in the list. The proposals for the failure
follow. When the orchestrator classifies the failure as a plugin-defect,
`/harden` still runs the repairs before it routes the failure to
`error-report`. A `--from-issue` run for an issue labeled `mcp-failure` skips
the orchestrator, so it drafts no repair.

## What harden can remove

- A rule with a dead reference (a path, tool, action, flag or step that no
  longer exists).
- A rule that a broader rule covers. `/harden` removes it, or merges it into
  the covering rule.

Each removal shows the full old text and needs approval. Under `--auto`, only
dead-reference removals apply, and only when the plugin's own scan lists each
dead token. A covering-rule removal or a merge waits as a pending item. A
repair of a pre-flight finding can also delete an id: it splits a rule that is
over 1024 bytes into smaller rules.

## Auto mode and pending edits

With `--auto`, `/harden` asks nothing. The action of each proposal decides what
happens:

| Proposal | Without `--auto` | With `--auto` |
|----------|------------------|---------------|
| add, strengthen, consolidate | ask | apply |
| repair of a pre-flight finding | ask | apply |
| repair or removal for a dead reference | ask | apply when the plugin's scan lists each dead token, else pending |
| removal for a covering rule | ask | pending |
| merge | ask | pending |
| any other proposal | ask | pending |

`/harden` writes nothing for a pending proposal. At the end of the run, it
stores the pending proposals of each rule section as one deferred item with the
source `harden-pending`. The id is `harden-pending-plan`,
`harden-pending-execute`, `harden-pending-dimensions` or
`harden-pending-copilot`. When the item with that id is already resolved, the
id gets a suffix: `harden-pending-plan-2`. A second `--auto` run adds its new
lines to the open item and does not repeat a line.

The summary that `/harden --auto` prints counts the pending proposals, and it
lists them under the id of their item:

    harden --auto: 2 auto-accepted, 0 reverted, 1 skipped, 1 pending, 0 not processed
    ...
    Pending (needs a person — stored as deferred item harden-pending-plan):
      [3] merge on plan-guardrails → dry, kiss — covered-by

When `/harden` cannot store an item, it lists the proposals in full under
`Pending (NOT stored — copy them by hand)`.

To apply the pending edits, run the replay command (see
[Examples](#examples)). The edits go through the normal approval prompt. The
replay run does not close the item. After it, `/harden` prints one line. The
line tells you to close the item with `/sdlc:deferred --resolve <id>` when
every edit is applied or rejected. Otherwise it says that the item stays open.
[`/deferred`](deferred.md) lists the item and prints the replay command. It
never files the item as a GitHub issue.

## Structured output

`/harden`'s preparation step returns five fields: `manifestPath`, `mode`,
`customInstructions` (the `[harden.instructions]` lists, one list for each
proposal surface), `preflightFindingCount` (the number of
[pre-flight findings](#pre-flight-findings)) and `next` (the step after the
call). The orchestrator reads the full manifest at `manifestPath` to build
proposals. The manifest holds the failure, the surfaces, the pre-flight
findings, the dead references and the same `customInstructions` map.

## Learnings stats

`learnings_log` has a `"stats"` action that summarizes
`.sdlc-v2/learnings/log.md` instead of returning it verbatim: total entry
count, counts by category (inferred from each entry's branch tag, e.g.
`feat`/`fix`) and by skill, the most-repeated recurring `Rule: ...` lessons
(`topPatterns`, capped at 10, sorted by count then recency), and how many of
the most recent 20 entries were failure-tagged (`recentFailures`). It never
errors on a missing or empty log — every count comes back zero instead.

## Skill recommendations

`/harden` mines `learnings_log`'s stats for recurring lesson patterns and
surfaces each one that has recurred at least 3 times as a candidate
skill/guardrail recommendation, with a coarse priority: `high` (seen 6+
times), `medium` (4-5 times), or `low` (3 times). A learning that recurs 3 or
more times becomes a proposal, unless an existing rule on one of the four
surfaces (plan guardrails, execute guardrails, review dimensions, Copilot
instructions) already covers it. The proposal goes through the same approval
prompt as any other. A recommendation is never an edit of its own: it is
evidence for a proposal on one of those four surfaces.

## Related skills

- [/error-report](../../plugins/sdlc/skills/error-report/SKILL.md) — Where
  `/harden` routes failures it classifies as plugin defects.
- [/setup](setup.md) — Author the initial guardrails and review dimensions
  that `/harden` later strengthens.
- [/review](review.md) — Review dimensions are one of the surfaces `/harden`
  can add to or strengthen.
- [/ship](ship.md) — Its opt-in `harden` step invokes `/harden` once per
  cluster of review findings.
- [/received-review](received-review.md) — Its Step 11.6 invokes `/harden`
  for clusters of review findings when `/ship`'s `harden` step is not
  configured.
- [/deferred](deferred.md) — Lists the pending rule edits that `/harden --auto`
  stores, and prints the command that replays them.
- [/plan](plan.md) and [/execute](execute.md) — Their guardrails are two of
  the surfaces `/harden` can strengthen.

## Tips and gotchas

- **Load State is mandatory.** `/harden` always starts by calling
  `prepare_orchestrator` (mode `harden`) to load the failure context and surface manifest. This
  must happen before any other tool call (unless invoked with
  `--from-learnings`, which first reads the log with `learnings_log` and then
  calls `prepare_orchestrator` once per entry, config-version check included).
  The same call loads the [custom instructions](#custom-instructions), and an
  invalid `[harden.instructions]` table stops the run here. A
  [pre-flight finding](#pre-flight-findings) does not stop the run: `/harden`
  prints the count, and it repairs the findings first.
- **Two removal cases.** `/harden` never relaxes a rule. It removes a rule for
  a dead reference or a covering rule. A split repair is the one other case in
  which a rule id goes: it replaces an oversize rule with smaller rules. Each
  preview shows the full old text. Custom instructions do not change this.
- **New rules name a class of failure.** A proposal that names one file or one
  tool as its condition is generalized to a class. When an existing rule
  already covers the class, `/harden` tightens that rule instead of adding a
  second one.
- **Guardrail proposals are checked before they are written.** For every
  action that edits a `plan`/`execute` guardrail, the order is the same: check
  in memory, write, then check on disk. The first check runs the proposal
  against the current config without writing. If it finds a problem (e.g. a
  description over 1024 bytes), `/harden` repairs it automatically —
  shortening the text, or splitting it into independent guardrails like
  `<id>-1`, `<id>-2` — and checks again, up to 2 repair rounds. Only a clean
  check is written to `config.toml`. The check on disk is a final safety net.
  A removal has no id left to check on disk, so it has no third step. One
  finding gets a different repair: when a proposal lowers the severity of a
  guardrail that is already on disk (`severity lowered from error to
  warning`), `/harden` sets the old severity back once and checks again. If
  that finding remains, `/harden` skips the proposal with no prompt, with or
  without `--auto`. Review dimensions and Copilot instructions have no
  in-memory check, so they keep the write-first-then-revert-on-failure flow. A
  merge of two review-dimension files edits the file that stays and checks it
  first. Only then does `/harden` delete the other file. If a later check
  fails, it writes the old text of the deleted file back.
- **Nothing is written without approval.** Each proposal is presented
  individually with a patch preview; you choose apply, skip, or cancel per
  proposal. For a repair, a removal and a merge, the preview also shows the
  full old text of the rule that changes or goes. For a merge, it is the rule
  that is removed. Cancelling stops the whole run without applying later
  proposals. For a repair, a removal or a merge, `/harden` reads the rule
  again before it asks. If the rule changed after the proposal was drafted, it
  skips the proposal. The one exception to the prompt is `--auto`: the caller
  passed it on purpose, so the proposals that the
  [`--auto` table](#auto-mode-and-pending-edits) allows are applied without a
  prompt and listed under "Auto-accepted". See the `/ship` note below for the
  callers that do this unattended.
- **Removals are logged.** The learnings entry that `/harden` writes at the end
  has a `Removed:` line. It names every rule id and file that an applied
  removal, merge or split repair deleted. The line is left out when nothing was
  deleted.
- **`--failure-text` and `--from-issue` are mutually exclusive.** Provide
  exactly one, not both.
- **`/ship` calls `/harden` in two ways.** When `harden` is in
  `ship.steps`, `/ship`'s own `harden` step (after `archive-openspec`, before `pr`)
  clusters the review findings, invokes `/harden` once per cluster (capped at
  5), and commits the edits as a separate commit. It then runs one fix pass
  for the deferred findings that are below the review threshold (see
  [/ship](ship.md)). It passes `--auto` when
  `/ship` runs with `--auto`; otherwise it asks once per cluster, and
  `/harden` asks per proposal. With the step configured, `/ship` passes
  `--no-harden` to [`/received-review`](received-review.md), so hardening runs
  once. Without the step, `/ship --auto` still reaches `/harden` through
  `/received-review`'s Step 11.6, which dispatches `harden --auto` for each
  cluster. Either unattended path edits your project's guardrails and review
  dimensions with no confirmation prompt. Each edit follows the `--auto` table,
  is still schema-validated, is still reverted if validation fails, and is
  listed under "Auto-accepted" in the output — but the point stands: an
  unattended `/ship --auto` can change your project's guardrails. The edits
  that the table holds back wait as pending deferred items.
- **Copilot mirrors follow the review dimensions.** The mirror of a dimension
  is `.github/instructions/<name>.instructions.md`. `/harden` keeps it in step
  with the dimension file:
  - A new dimension gets a mirror. Existing dimensions are not retroactively
    mirrored.
  - A repair, a strengthen or a consolidate renders the mirror again, but only
    when the mirror exists. The render overwrites the file, so rows that you
    added to a mirror by hand are lost.
  - A removal, or the removed file of a merge, deletes the mirror when it
    exists. A merge also renders the mirror of the file that stays, when it
    exists.
  - A dimension `name` that is not a bare name (it is empty, or it has `/`,
    `\` or `..`) gets no mirror step. `/harden` prints `Copilot mirror skipped: unsafe name
    <name>`, and the dimension edit stays applied.
- **Three proposals are skipped before the prompt.** `/harden` does not ask and
  writes nothing for these proposals:
  - A removal or a merge that names `_common.md`. That file holds the common
    review text and is not a dimension.
  - A merge that is malformed: it names more than one file to remove, or the
    file it merges into is not the file it edits.
  - A repair for a guardrail with no usable id (no id, an id that is not text,
    or an empty id). `/harden` cannot write a rule without an id. Nothing stores
    this repair, so the message is its only record. Fix the rule by hand.

  Without `--auto`, `/harden` tells you about the skip. With `--auto`, the
  summary lists the proposal under `Skipped` as `_common.md is not a
  dimension`, `malformed merge` or `not repairable: no rule id`.
- **A failed restore stops the run.** When `/harden` deletes a review-dimension
  file and a later check finds a problem, it writes the old text back. If that
  write fails too, `/harden` prints the old text in full and the line `NOT
  restored: <file> — copy the text above back by hand`. Copy the printed text
  back into the file yourself. `/harden` processes no later proposal. With
  `--auto`, the summary lists them under `Not processed`. A failed update of a
  Copilot mirror stops the run in the same way: the dimension file stays
  written and validated, and `/harden` asks you to fix the mirror by hand.
- **An unreadable review-dimensions directory stops the run.** `/harden` reads
  that directory in its pre-flight check. When it cannot read it, the run stops
  with an error. `/harden` does not offer `error-report`, because the cause is
  your project and not the plugin. In `--from-learnings` mode the error stops
  the whole triage run, and `/harden` removes no learnings entry.
