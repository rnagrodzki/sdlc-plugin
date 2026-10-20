# /harden

Analyze a pipeline failure and propose user-approved edits to the project's
hardening surfaces — plan/execute guardrails, review dimensions, or Copilot
instructions — so the same class of failure is caught earlier next time.
Harden adds, tightens and repairs rules. It never relaxes a rule. It removes a
rule only in three cases: a dead reference, a covering rule, or a pre-flight
repair that splits an oversize rule. Each removal needs approval, except under
`--auto`: see [What harden can remove](#what-harden-can-remove).

## When to use

- A pipeline step (`/plan`, `/execute`, `/review`, ...) just failed and you
  want to prevent the same failure from recurring.
- You want to turn a one-off mistake into a durable guardrail or review
  dimension instead of just fixing the immediate symptom.
- Another skill's failure-handling menu offered a "harden" option and you
  picked it.

## Syntax

    /harden --failure-text <text> --skill <name> [options]
    /harden --from-issue <num> --skill <name> [options]
    /harden --from-learnings

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--failure-text <string>` | The failure to analyze, as free text. Required unless `--from-issue` or `--from-learnings` is used; mutually exclusive with both. The special value `Pending rule edits: <id>` replays the rule edits of the deferred item `<id>` (see [Auto mode and pending edits](#auto-mode-and-pending-edits)). | — |
| `--from-issue <num>` | Analyze a GitHub issue instead of inline text. Mutually exclusive with `--failure-text` and `--from-learnings`. | — |
| `--from-learnings` | Triage every learnings entry that `/harden` did not write itself, one orchestrator pass for each entry (see [Learnings triage](#learnings-triage)). Mutually exclusive with `--failure-text` and `--from-issue`. Not valid with `--auto`. | off |
| `--skill <name>` | Name of the skill that failed. Required with `--failure-text` and `--from-issue`. Ignored with `--from-learnings`: `/harden` reads the skill name from the header of each entry. | — |
| `--step <s>` | Step name within the failing skill, if known. | — |
| `--operation <op>` | Operation being performed when the failure occurred. | — |
| `--exit-code <code>` | Exit code or HTTP status from the failure, if any. | — |
| `--error-type <type>` | Category of error, if known. | — |
| `--user-intent <text>` | What the user was trying to accomplish. | — |
| `--args-string <text>` | The invocation arguments that led to the failure. | — |
| `--auto` | Run without prompts, and list each applied proposal under "Auto-accepted" in the output. Two callers pass it: [`/received-review --auto`](received-review.md), when its Step 11.6 dispatches `/harden` (subagent dispatch, where `AskUserQuestion` is not available), and [`/ship --auto`](ship.md)'s own `harden` step, which forwards the flag because the run is unattended. Applies add, strengthen and consolidate, and the repairs and dead-reference removals that the plugin's own checks prove. Merges, covered-by removals and unproven repairs or removals wait as one deferred item for each rule section (see [Auto mode and pending edits](#auto-mode-and-pending-edits)). Each edit is still validated, and `/harden` reverts it on failure. Custom instructions still apply (see [Custom instructions](#custom-instructions)). Never files an `error-report` issue. Not valid with `--from-learnings`. | off |

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
  rule outside the three removal cases (see
  [What harden can remove](#what-harden-can-remove)).
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
the orchestrator, so it drafts no repair. When that run has findings, it
prints a second line:

    Pre-flight findings not repaired: the plugin-defect hint skips the orchestrator.

## What harden can remove

`/harden` removes a rule in three cases only:

- A rule with a dead reference (a path, tool, action, flag or step that no
  longer exists).
- A rule that a broader rule covers. `/harden` removes it, or merges it into
  the covering rule.
- A guardrail that is over 1024 bytes. A repair of this pre-flight finding
  splits it into smaller rules, and the old id goes.

Each removal shows the full old text and needs approval. Under `--auto`, a
dead-reference removal applies only when the plugin's own scan lists each
dead token for that same rule (same file and, for a guardrail, same id). A
split applies only when the pre-flight check reported that rule. A
covering-rule removal or a merge waits as a pending item.

## Auto mode and pending edits

With `--auto`, `/harden` asks nothing. The action of each proposal decides what
happens:

| Proposal | Without `--auto` | With `--auto` |
|----------|------------------|---------------|
| add, strengthen, consolidate | ask | apply |
| repair of a pre-flight finding | ask | apply when the pre-flight check reported that rule, else pending |
| repair or removal for a dead reference | ask | apply when the plugin's scan lists each dead token for that same rule, else pending |
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
`Pending (NOT stored — copy them by hand)`. It does the same for a proposal
with an id that fails the shape check (for example, an id with a line break),
with the reason `unsafe id: <value>`. The ids come from failure text that can
be untrusted, and a replay reads each stored line back as an edit.

The summary also lists, under `Dropped drafts`, each draft that the
orchestrator dropped because it named one case and not a class of failure.

To apply the pending edits, run the replay command (see
[Examples](#examples)). The edits go through the normal approval prompt. The
replay run does not close the item. After it, `/harden` prints one line:

- Close the item with `/sdlc:deferred --resolve <id>`: at least one edit was
  replayed, and you applied or skipped each one.
- No rule edit was drafted: the id is wrong or the item is closed. Check the
  id with `/sdlc:deferred --list`.
- The item stays open: an edit is pending again, was reverted, or was skipped
  by a check (for example, the rule changed after the edit was drafted).
[`/deferred`](deferred.md) lists the item and prints the replay command. It
never files the item as a GitHub issue.

## Learnings triage

    /harden --from-learnings

`/harden` reads `.sdlc-v2/learnings/log.md` and triages each entry that it
did not write itself (entries with the header `## YYYY-MM-DD — harden:` are
skipped). It needs no `--skill`: it reads the skill name from the header of
each entry. It is not valid with `--auto`, because it deletes learnings
entries and needs a person.

- **One orchestrator pass for each entry.** `/harden` loads each entry as a
  failure and classifies it. Then it presents the proposals of each entry
  with the normal approval prompt: user-code entries first, then ambiguous,
  then plugin-defect.
- **The custom instructions and the `Pre-flight findings:` line print once**,
  after the first entry that loads. Every entry reads the same config.
- **A pre-flight repair or a dead-reference proposal shows once.** Each entry
  drafts the same proposals for them, so `/harden` shows each one with the
  first entry only.
- **Applied entries are removed.** An entry with at least one applied
  proposal is removed from the log, in one call at the end. A pre-flight
  repair or a dead-reference proposal does not count, because it does not
  fix the entry.
- **Cancel removes nothing.** A **cancel** answer ends the run. No learnings
  entry is removed, and `/harden` writes no learnings entry of its own.
- **A config error stops the whole run.** An invalid `[harden.instructions]`
  table, a config version error, or a `config.toml` that cannot be read stops
  the triage at the first entry. No entry is removed.

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
- **Three removal cases.** `/harden` never relaxes a rule. It removes a rule
  for a dead reference or a covering rule, or in a split repair, which
  replaces an oversize rule with smaller rules. Each preview shows the full
  old text. Custom instructions do not change this.
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
  A removal has no id left to check on disk, so it has no third step. A
  removal, a merge and a split delete a guardrail with a JSON `null` in
  `setup_write_sections`. The tool refuses that delete when the guardrail
  table in `config.toml` holds a comment line. `/harden` then restores
  `config.toml` and skips the proposal (`write failed: <error>` under
  `--auto`). Delete the comment line by hand, then run `/harden` again. One
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
    when the mirror exists. Each render ends the mirror with a marker line,
    for example
    `<!-- sdlc:generated sha256=9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08 -->`.
    A marker is valid when its hash is the lowercase hex SHA-256 (64
    characters) of all text above the marker line, with each `\r\n` read as
    `\n`. The render replaces a mirror only when the hash still matches the
    text above it.
  - The render refuses a mirror and leaves it as it is in these cases:
    - The mirror has no marker line at the end. The text is hand-written, or
      an older sdlc version wrote the mirror before the marker existed.
    - The marker hash does not match the text above it, or the hash is not 64
      lowercase hex characters. The text was changed by hand.
    - The mirror is over 1 MiB. The render does not read it.
  - On a refusal, `/harden` prints `Copilot mirror skipped: hand-written
    content in <path>` and continues with the next proposal. Under `--auto`,
    `/harden` also appends `; Copilot mirror skipped: hand-written content in
    {path}` to the `Auto-accepted` line of that proposal. To adopt a refused
    mirror, move its hand-written rows into the dimension file, then call
    `dimensions_render_instructions` for that dimension with
    `overwrite: true`. For a mirror that an older sdlc version wrote and
    nobody edited, call with `overwrite: true` directly. `/harden` never
    passes `overwrite: true`.
  - A folder or other special file at the mirror path is an error, not a
    refusal. The render fails, and `/harden` halts the loop. The error tells
    you to remove the folder or special file at that path.
  - A mirror that `/harden` edits itself (the `copilot-instructions` surface)
    has no valid marker line after the edit. The next render of that mirror is
    refused until you adopt it with `overwrite: true`.
  - A removal, or the removed file of a merge, deletes the mirror when it
    exists. A merge also renders the mirror of the file that stays, when it
    exists.
  - A dimension `name` that is not a bare name (it is empty, or it has `/`,
    `\` or `..`) gets no mirror step. `/harden` prints `Copilot mirror skipped: unsafe name
    <name>`, and the dimension edit stays applied. Under `--auto`, `/harden`
    also appends `; Copilot mirror skipped: unsafe name <name>` to the
    `Auto-accepted` line of that proposal.
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

  Two more skips come after approval, in the guardrail check and write. The
  check tool can reject the input (for example, a removed id that is not in
  `config.toml`): the summary line is `check error: <error>`. The write can
  fail (for example, the comment-line refusal above): `/harden` restores
  `config.toml`, and the summary line is `write failed: <first error>`.
- **A failed restore stops the run.** When `/harden` deletes a review-dimension
  file and a later check finds a problem, it writes the old text back. If that
  write fails too, `/harden` prints the old text in full and the line `NOT
  restored: <file> — copy the text above back by hand`. Copy the printed text
  back into the file yourself. `/harden` processes no later proposal. With
  `--auto`, the summary lists them under `Not processed`. A guardrail write
  that fails and then cannot be undone stops the run in the same way:
  `/harden` prints the old `config.toml` in full. A failed update of a
  Copilot mirror stops the run in the same way: the dimension file stays
  written and validated, and `/harden` asks you to fix the mirror by hand.
  A refused mirror update is not a failed update. It does not stop the run
  (see the Copilot mirror bullet above).
- **An unreadable review-dimensions directory stops the run.** `/harden` reads
  that directory in its pre-flight check. When it cannot read it, the run stops
  with an error. `/harden` does not offer `error-report`, because the cause is
  your project and not the plugin. In `--from-learnings` mode the error stops
  the whole triage run, and `/harden` removes no learnings entry.
