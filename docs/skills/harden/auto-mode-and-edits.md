# Flags and rule edits

Read this page when you run `/sdlc:harden` with flags, when another skill runs
it with `--auto`, or when you want to know how `/sdlc:harden` checks an edit
before and after the write.

```text
/sdlc:harden --failure-text <text> --skill <name> [options]
/sdlc:harden --from-issue <num> --skill <name> [options]
/sdlc:harden --from-learnings
```

Give exactly one of the three inputs. The [overview page](../harden.md#example)
shows an example.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--failure-text <string>` | The failure to analyze, as free text. Required unless `--from-issue` or `--from-learnings` is used; mutually exclusive with both. The special value `Pending rule edits: <id>` replays the rule edits of the deferred item `<id>` (see [Auto mode and pending edits](#auto-mode-and-pending-edits)). | — |
| `--from-issue <num>` | Analyze a GitHub issue instead of inline text. Mutually exclusive with `--failure-text` and `--from-learnings`. | — |
| `--from-learnings` | Triage every learnings entry that `/sdlc:harden` did not write itself, one orchestrator pass for each entry (see [Learnings triage](./learnings.md#learnings-triage)). Mutually exclusive with `--failure-text` and `--from-issue`. Not valid with `--auto`. | off |
| `--skill <name>` | Name of the skill that failed. Required with `--failure-text` and `--from-issue`. Ignored with `--from-learnings`: `/sdlc:harden` reads the skill name from the header of each entry. | — |
| `--step <s>` | Step name within the failing skill, if known. | — |
| `--operation <op>` | Operation being performed when the failure occurred. | — |
| `--exit-code <code>` | Exit code or HTTP status from the failure, if any. | — |
| `--error-type <type>` | Category of error, if known. | — |
| `--user-intent <text>` | What the user was trying to accomplish. | — |
| `--args-string <text>` | The invocation arguments that led to the failure. | — |
| `--auto` | Run without prompts, and list each applied proposal under "Auto-accepted" in the output. Two callers pass it: [`/sdlc:received-review --auto`](../received-review.md), when its Step 11.6 dispatches `/sdlc:harden` (subagent dispatch, where `AskUserQuestion` is not available), and [`/sdlc:ship --auto`](../ship.md)'s own `harden` step, which forwards the flag because the run is unattended. Applies add, strengthen and consolidate, and the repairs and dead-reference removals that the plugin's own checks prove. Merges, covered-by removals and unproven repairs or removals wait as one deferred item for each rule section (see [Auto mode and pending edits](#auto-mode-and-pending-edits)). Each edit is still validated, and `/sdlc:harden` reverts it on failure. Custom instructions still apply (see [Custom instructions](./custom-instructions.md)). Never files an `error-report` issue. Not valid with `--from-learnings`. A follow-up instruction can still ask a critical question (see [Follow-up instructions](#follow-up-instructions)). | off |
| `--no-instructions` | Do not run the follow-up instructions of the skill. Ship passes this flag, because ship runs the lists itself. Independent of `--auto`. No effect with `--from-learnings`, because that mode runs no list. See [Follow-up instructions](#follow-up-instructions). | off |

## Pre-flight findings

Before it drafts proposals, `/sdlc:harden` checks the guardrails and review
dimensions that already exist. A finding does not stop the run. `/sdlc:harden`
prints one line, and it drafts one repair proposal for each finding:

    Pre-flight findings: <N> (repair proposals come first)

The repair proposals come first in the list. The proposals for the failure
follow. When the orchestrator classifies the failure as a plugin-defect,
`/sdlc:harden` still runs the repairs before it routes the failure to
`error-report`. A `--from-issue` run for an issue labeled `mcp-failure` skips
the orchestrator, so it drafts no repair. When that run has findings, it prints
a second line:

    Pre-flight findings not repaired: the plugin-defect hint skips the orchestrator.

One pre-flight problem stops the run. `/sdlc:harden` reads the
review-dimensions directory in its pre-flight check. When it cannot read the
directory, the run stops with an error. `/sdlc:harden` does not offer
`error-report`, because the cause is your project and not the plugin. In
`--from-learnings` mode the error stops the whole triage run, and
`/sdlc:harden` removes no learnings entry.

## What harden can remove

`/sdlc:harden` removes a rule in three cases only:

- A rule with a dead reference (a path, tool, action, flag or step that no
  longer exists).
- A rule that a broader rule covers. `/sdlc:harden` removes it, or merges it into
  the covering rule.
- A guardrail that is over 1024 bytes. A repair of this pre-flight finding
  splits it into smaller rules, and the old id goes.

Each removal shows the full old text and needs approval. Under `--auto`, a
dead-reference removal applies only when the plugin's own scan lists each dead
token for that same rule (same file and, for a guardrail, same id). A split
applies only when the pre-flight check reported that rule. A covering-rule
removal or a merge waits as a pending item.

The learnings entry that `/sdlc:harden` writes at the end has a `Removed:`
line. It names every rule id and file that an applied removal, merge or split
repair deleted. The line is left out when nothing was deleted.

## Auto mode and pending edits

With `--auto`, `/sdlc:harden` asks nothing about a proposal. The action of each
proposal decides what happens:

| Proposal | Without `--auto` | With `--auto` |
|----------|------------------|---------------|
| add, strengthen, consolidate | ask | apply |
| repair of a pre-flight finding | ask | apply when the pre-flight check reported that rule, else pending |
| repair or removal for a dead reference | ask | apply when the plugin's scan lists each dead token for that same rule, else pending |
| removal for a covering rule | ask | pending |
| merge | ask | pending |
| any other proposal | ask | pending |

`/sdlc:harden` writes nothing for a pending proposal. At the end of the run, it
stores the pending proposals of each rule section as one deferred item with the
source `harden-pending`. The id is `harden-pending-plan`,
`harden-pending-execute`, `harden-pending-dimensions` or
`harden-pending-copilot`. When the item with that id is already resolved, the
id gets a suffix: `harden-pending-plan-2`. A second `--auto` run adds its new
lines to the open item and does not repeat a line.

The summary that `/sdlc:harden --auto` prints counts the pending proposals, and
it lists them under the id of their item:

    harden --auto: 2 auto-accepted, 0 reverted, 1 skipped, 1 pending, 0 not processed
    ...
    Pending (needs a person — stored as deferred item harden-pending-plan):
      [3] merge on plan-guardrails → dry, kiss — covered-by

When `/sdlc:harden` cannot store an item, it lists the proposals in full under
`Pending (NOT stored — copy them by hand)`. It does the same for a proposal
with an id that fails the shape check (for example, an id with a line break),
with the reason `unsafe id: <value>`. The ids come from failure text that can
be untrusted, and a replay reads each stored line back as an edit.

The summary also lists, under `Dropped drafts`, each draft that the
orchestrator dropped because it named one case and not a class of failure.

To apply the pending edits, run the replay command (see [More
examples](../harden.md#more-examples)). The edits go through the normal
approval prompt. The replay run does not close the item. After it,
`/sdlc:harden` prints one line:

- Close the item with `/sdlc:deferred --resolve <id>`: at least one edit was
  replayed, and you applied or skipped each one.
- No rule edit was drafted: the id is wrong or the item is closed. Check the
  id with `/sdlc:deferred --list`.
- The item stays open: an edit is pending again, was reverted, or was skipped
  by a check (for example, the rule changed after the edit was drafted).

[`/sdlc:deferred`](../deferred.md) lists the item and prints the replay
command. It never files the item as a GitHub issue.

### Unattended runs from ship

[`/sdlc:ship`](../ship.md) runs `/sdlc:harden` in two ways. When `harden` is in
`ship.steps`, `/sdlc:ship`'s own `harden` step (after `archive-openspec`,
before `pr`) clusters the review findings, invokes `/sdlc:harden` once per
cluster (capped at 5), and commits the edits as a separate commit. It then runs
one fix pass for the deferred findings that are below the review threshold (see
[/sdlc:ship](../ship.md)). It passes `--auto` when `/sdlc:ship` runs with
`--auto`; otherwise it asks once per cluster, and `/sdlc:harden` asks per
proposal. With the step configured, `/sdlc:ship` passes `--no-harden` to
[`/sdlc:received-review`](../received-review.md), so hardening runs once.

Without the step, `/sdlc:ship --auto` still reaches `/sdlc:harden` through
`/sdlc:received-review`'s Step 11.6, which dispatches `harden --auto` for each
cluster.

Either unattended path edits your project's guardrails and review
dimensions with no confirmation prompt. Each edit follows the `--auto` table,
is still schema-validated, is still reverted if validation fails, and is listed
under "Auto-accepted" in the output — but the point stands: an unattended
`/sdlc:ship --auto` can change your project's guardrails. The edits that the
table holds back wait as pending deferred items.

## Approval and checks of an edit

### Nothing is written without approval

Each proposal is presented individually with a patch preview; you choose apply,
skip, or cancel per proposal. For a repair, a removal and a merge, the preview
also shows the full old text of the rule that changes or goes. For a merge, it
is the rule that is removed. Cancelling stops the whole run without applying
later proposals. For a repair, a removal or a merge, `/sdlc:harden` reads the
rule again before it asks. If the rule changed after the proposal was drafted,
it skips the proposal. The one exception to the prompt is `--auto`: the caller
passed it on purpose, so the proposals that the [`--auto`
table](#auto-mode-and-pending-edits) allows are applied without a prompt and
listed under "Auto-accepted". See [Unattended runs from
ship](#unattended-runs-from-ship) for the callers that do this unattended.

### New rules name a class of failure

A proposal that names one file or one tool as its condition is generalized to a
class. When an existing rule already covers the class, `/sdlc:harden` tightens
that rule instead of adding a second one.

### Guardrail checks

For every action that edits a `plan`/`execute` guardrail, the order is the
same: check in memory, write, then check on disk. The first check runs the
proposal against the current config without writing. If it finds a problem
(e.g. a description over 1024 bytes), `/sdlc:harden` repairs it automatically —
shortening the text, or splitting it into independent guardrails like `<id>-1`,
`<id>-2` — and checks again, up to 2 repair rounds. Only a clean check is
written to `config.toml`. The check on disk is a final safety net. A removal
has no id left to check on disk, so it has no third step. A removal, a merge
and a split delete a guardrail with a JSON `null` in `setup_write_sections`.
The tool refuses that delete when the guardrail table in `config.toml` holds a
comment line. `/sdlc:harden` then restores `config.toml` and skips the proposal
(`write failed: <error>` under `--auto`). Delete the comment line by hand, then
run `/sdlc:harden` again. One finding gets a different repair: when a proposal
lowers the severity of a guardrail that is already on disk (`severity lowered
from error to warning`), `/sdlc:harden` sets the old severity back once and
checks again. If that finding remains, `/sdlc:harden` skips the proposal with
no prompt, with or without `--auto`. Review dimensions and Copilot instructions
have no in-memory check, so they keep the write-first-then-revert-on-failure
flow. A merge of two review-dimension files edits the file that stays and
checks it first. Only then does `/sdlc:harden` delete the other file. If a
later check fails, it writes the old text of the deleted file back.

## Copilot mirrors

The mirror of a dimension is `.github/instructions/<name>.instructions.md`.
`/sdlc:harden` keeps it in step with the dimension file:

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
- On a refusal, `/sdlc:harden` prints `Copilot mirror skipped: hand-written
  content in <path>` and continues with the next proposal. Under `--auto`,
  `/sdlc:harden` also appends `; Copilot mirror skipped: hand-written content in
  {path}` to the `Auto-accepted` line of that proposal. To adopt a refused
  mirror, move its hand-written rows into the dimension file, then call
  `dimensions_render_instructions` for that dimension with
  `overwrite: true`. For a mirror that an older sdlc version wrote and
  nobody edited, call with `overwrite: true` directly. `/sdlc:harden` never
  passes `overwrite: true`.
- A folder or other special file at the mirror path is an error, not a
  refusal. The render fails, and `/sdlc:harden` halts the loop. The error tells
  you to remove the folder or special file at that path.
- A mirror that `/sdlc:harden` edits itself (the `copilot-instructions` surface)
  has no valid marker line after the edit. The next render of that mirror is
  refused until you adopt it with `overwrite: true`.
- A removal, or the removed file of a merge, deletes the mirror when it
  exists. A merge also renders the mirror of the file that stays, when it
  exists.
- A dimension `name` that is not a bare name (it is empty, or it has `/`,
  `\` or `..`) gets no mirror step. `/sdlc:harden` prints `Copilot mirror skipped: unsafe name
  <name>`, and the dimension edit stays applied. Under `--auto`, `/sdlc:harden`
  also appends `; Copilot mirror skipped: unsafe name <name>` to the
  `Auto-accepted` line of that proposal.

## Skipped proposals

`/sdlc:harden` does not ask and writes nothing for these proposals:

- A removal or a merge that names `_common.md`. That file holds the common
  review text and is not a dimension.
- A merge that is malformed: it names more than one file to remove, or the
  file it merges into is not the file it edits.
- A repair for a guardrail with no usable id (no id, an id that is not text,
  or an empty id). `/sdlc:harden` cannot write a rule without an id. Nothing stores
  this repair, so the message is its only record. Fix the rule by hand.

Without `--auto`, `/sdlc:harden` tells you about the skip. With `--auto`, the
summary lists the proposal under `Skipped` as `_common.md is not a dimension`,
`malformed merge` or `not repairable: no rule id`.

Two more skips come after approval, in the guardrail check and write. The check
tool can reject the input (for example, a removed id that is not in
`config.toml`): the summary line is `check error: <error>`. The write can fail
(for example, the comment-line refusal in [Guardrail
checks](#guardrail-checks)): `/sdlc:harden` restores `config.toml`, and the
summary line is `write failed: <first error>`.

## A failed restore stops the run

When `/sdlc:harden` deletes a review-dimension file and a later check finds a
problem, it writes the old text back. If that write fails too, `/sdlc:harden`
prints the old text in full and the line `NOT restored: <file> — copy the text
above back by hand`. Copy the printed text back into the file yourself.
`/sdlc:harden` processes no later proposal. With `--auto`, the summary lists
them under `Not processed`. A guardrail write that fails and then cannot be
undone stops the run in the same way: `/sdlc:harden` prints the old
`config.toml` in full. A failed update of a Copilot mirror stops the run in the
same way: the dimension file stays written and validated, and `/sdlc:harden`
asks you to fix the mirror by hand. A refused mirror update is not a failed
update. It does not stop the run (see [Copilot mirrors](#copilot-mirrors)).

## Follow-up instructions

`/sdlc:harden` runs the `harden` list of the `[instructions]` config at the
end of a successful run, for example a post to a team channel. This list is
not the `[harden.instructions]` table: see [Custom
instructions](./custom-instructions.md#not-the-same-as-follow-up-instructions).
[Follow-up instructions](../../instructions.md) has the config, the merge rule
and the question rules.

| Run | Flow |
|---|---|
| Before | Step 0 → … → Step 7 learning capture |
| After | Step 0 (with the resolve) → … → Step 7 learning capture → Step 8 run the list → summary line |

- The list runs only after Step 7 ends a successful run, in interactive and
  `--auto` mode.
- The run mode is the `--auto` flag of this run. Under `--auto`, a critical
  question that the `criticalQuestions` flag allows is the one prompt of the
  run. With no way to ask (a subagent run), the question is not allowed, and
  `onQuestionNotAllowed` applies.
- A failed instruction does not fail the run. An empty list prints no
  instructions line.

| Exit route | List runs | What you see |
|---|---|---|
| config error in Step 0 | no | the error and its fix. No step after Step 0 starts. Fix the config, then run the skill again. |
| `--from-learnings` | no. The list belongs to a failure run. | the end of the triage run |
| `--no-instructions` | no | the end of the run |
| a stop in Step 5 (cancel or halt) | no | the existing text of that stop |
| the plugin-defect route (Step 6) | no | the existing text of that route |
| Step 7 ends a successful run | yes | `Instructions (harden): N total, X done, Y failed, Z not run.` |
| you interrupt the list | partial | `Run the remaining instructions by hand. A new run of the skill runs the whole list again unless you pass --no-instructions.` |
