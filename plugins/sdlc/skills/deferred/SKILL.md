---
name: deferred
description: "Use this skill to list and triage deferred items: review findings and issue drafts that earlier pipeline runs recorded but did not fix. With no arguments it shows every open item, then lets you file chosen items as GitHub issues (label deferred-followup) or resolve them without filing. --list only prints the open items. --resolve <id> resolves one item without filing. Arguments: [--list | --resolve <id>]. Triggers on: deferred items, deferred follow-ups, deferred backlog, triage deferred, open deferred issues, list deferred, resolve deferred."
user-invocable: true
argument-hint: "[--list | --resolve <id>]"
model: sonnet
---

# Deferred Follow-ups (SDLC)

Some findings are recorded during a pipeline run but not fixed: review findings below the review threshold, issue drafts raised by the execute step, and findings `/received-review` left unfixed (`wont-fix`, `disagree`, `needs-direction`). Each one is written to `<MAIN_ROOT>/.sdlc-v2/history/deferred.json` the moment it is found, so it normally survives an early exit and state-file cleanup. That write is best-effort: the recording tool warns instead of failing, so an item whose write failed is not here. This skill shows the open items and lets you triage them. You can file an item as a GitHub issue, or close it without filing.

**Announce at start:** "I'm using deferred (sdlc v{sdlc_version})." - extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

**Render every `display` field verbatim.** A `ship_state` response with a `display` field is pre-formatted markdown. Print it as it is. Never paraphrase or reformat it.

Companion file: [`reference.md`](reference.md) holds the triage flow. Ship Step 10b follows the same file.

---

## Step 1: Parse arguments

| Arguments | Mode |
|---|---|
| none | Triage (Step 2c) |
| `--list` | List (Step 2a) |
| `--resolve <id>` | Resolve one item (Step 2b) |

Stop with a one-line usage message, `/sdlc:deferred [--list | --resolve <id>]`, when both flags are given, when `--resolve` has no id, or when any other argument appears. Do not call any tool first.

## Step 2: Run the mode

### 2a. `--list`

1. Call `ship_state({action:"deferred_list"})`. It returns `{issues, openCount}`. `issues` includes resolved items and has no `display`, so this mode formats its own output.
2. If `openCount` is 0, print exactly `No deferred items open.` and stop. Print no table.
3. Otherwise print a markdown table of the items whose `status` is `open`, with the columns `ID`, `Priority`, `Reason`, `Created` and `Description`. Fill `Reason` from the item's `reason`, falling back to its `source` when `reason` is unset — `source` is `review-below-threshold` on every `ship_state defer` record, so it is not the reason on its own. Put high priority first, and the oldest first within one priority. When an item has `file`, add ` (file:line)` after its description, and drop `:line` when `line` is 0. Do not print resolved items.
4. Stop. Ask nothing and change nothing.

### 2b. `--resolve <id>`

1. Call `ship_state({action:"deferred_list"})` and look for this id. `deferred_resolve` succeeds on an already-resolved id, so check the status first.
2. If the item is there and its `status` is not `open`, print `<id> is already resolved.` and stop. Call nothing else.
3. Otherwise call `ship_state({action:"deferred_resolve", detail:{id:"<id>"}})`. It returns `{ok, id}`.
4. On success, print `Resolved <id> without filing an issue.` and stop.
5. On an error, print the tool's message and its suggestion as they are, and stop. An unknown id means the id is wrong: tell the user to run `/sdlc:deferred --list` to see the open ids.

This mode never asks a question and never calls `gh`.

### 2c. Triage (no arguments)

1. Call `ship_state({action:"deferred_propose_followups"})`. It returns `{openCount, groups, display}`.
2. If `openCount` is 0, print exactly `No deferred items open.` and stop. Ask nothing. Print no table.
3. Render `display` verbatim.
4. Follow the triage flow in [`reference.md`](reference.md): the question, the choice of items, then filing or resolving, then the close-out line.

---

## Rules

- Before every `gh issue create`, show the full drafted title and body in chat and get the user's approval. No approval, no issue.
- Every issue gets the label `deferred-followup`, unless the user chose to file without it because the label does not exist yet.
- `gh label create` writes to the shared remote repo. Never run it without asking the user first.
- Never append an AI-tool attribution line ("Generated with Claude Code" or similar) to an issue body.
- Call `deferred_resolve` for an item only after its issue was created, or when the user chose to resolve it without filing.
- Never Read or edit the deferred store file by hand. Every change goes through `ship_state`.
- Triage mode has no auto mode. It always asks before it acts.

Related: [`/ship`](../ship/SKILL.md) runs the same triage at the end of a pipeline run (Step 10b).
