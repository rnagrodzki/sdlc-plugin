---
name: deferred
description: "Use this skill to list and triage deferred items: review findings and issue drafts that earlier pipeline runs recorded but did not fix. With no arguments it shows every open item, then lets you file chosen items as GitHub issues (label deferred-followup) or resolve them without filing. --list only prints the open items. --resolve <id> resolves one item without filing. Arguments: [--list | --resolve <id>]. Triggers on: deferred items, deferred follow-ups, deferred backlog, triage deferred, open deferred issues, list deferred, resolve deferred."
user-invocable: true
argument-hint: "[--list | --resolve <id>]"
model: sonnet
---

# Deferred Follow-ups (SDLC)

Some findings are recorded during a pipeline run but not fixed: review findings below the review threshold, and issue drafts raised by the execute step. Each one is stored the moment it is found, in .sdlc-v2/history/deferred.json, so it survives an early exit and state-file cleanup. This skill shows the open items and lets you triage them. You can file an item as a GitHub issue, or close it without filing.

**Announce at start:** "I'm using deferred (sdlc v{sdlc_version})." - extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

**Render every `display` field verbatim.** A `ship_state` response with a `display` field is pre-formatted markdown. Print it as it is. Never paraphrase or reformat it.

Companion file: [`reference.md`](reference.md) holds the triage flow. Ship Step 10b follows the same file, so the flow is written down once.

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
3. Otherwise print a markdown table of the items whose `status` is `open`, with the columns `ID`, `Priority`, `Source`, `Created` and `Description`. Put high priority first, and the oldest first within one priority. When an item has `file`, add ` (file:line)` after its description, and drop `:line` when `line` is 0. Do not print resolved items.
4. Stop. Ask nothing and change nothing.

### 2b. `--resolve <id>`

1. Call `ship_state({action:"deferred_resolve", detail:{id:"<id>"}})`. It returns `{ok, id}`.
2. On success, print `Resolved <id> without filing an issue.` and stop.
3. On an error, print the tool's message and its suggestion as they are, and stop. An unknown id means the id is wrong or already resolved: tell the user to run `/sdlc:deferred --list` to see the open ids.

This mode never asks a question and never calls `gh`.

### 2c. Triage (no arguments)

1. Call `ship_state({action:"deferred_propose_followups"})`. It returns `{openCount, groups, display}`.
2. If `openCount` is 0, print exactly `No deferred items open.` and stop. Ask nothing. Print no table.
3. Render `display` verbatim.
4. Follow the triage flow in [`reference.md`](reference.md): the question, the choice of items, then filing or resolving, then the close-out line.

---

## Rules

- Before every `gh issue create`, show the full drafted title and body in chat and get the user's approval. No approval, no issue.
- Every issue gets the label `deferred-followup`.
- Never append an AI-tool attribution line ("Generated with Claude Code" or similar) to an issue body.
- Call `deferred_resolve` for an item only after its issue was created, or when the user chose to resolve it without filing.
- Never Read or edit the deferred store file by hand. Every change goes through `ship_state`.
- Triage mode has no auto mode. It always asks before it acts.

Related: [`/ship`](../ship/SKILL.md) runs the same triage at the end of a pipeline run (Step 10b).
