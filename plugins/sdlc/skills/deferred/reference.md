# Deferred-item triage flow

Two callers follow this flow:

- `/sdlc:deferred` in its default mode (see [`SKILL.md`](SKILL.md)).
- The ship skill, Step 10b (see [`../ship/SKILL.md`](../ship/SKILL.md)).

`/sdlc:deferred --list` and `/sdlc:deferred --resolve <id>` do not use this flow. `SKILL.md` handles them.

## Before the flow starts

The caller has already done three things:

1. Called `ship_state({action:"deferred_propose_followups"})`.
2. Handled the case where `openCount` is 0. The flow only starts when `openCount` is 1 or more.
3. Rendered `display` verbatim.

Each caller owns those three steps, because the two callers differ:

| | `/sdlc:deferred` | ship Step 10b |
|---|---|---|
| `openCount` is 0 | Prints exactly `No deferred items open.` and stops. | Prints nothing and moves on. |
| Step 1 onward (the question and all that follows) | Always runs. | Skipped when ship runs in its automatic mode. `display` is still rendered. |

## The data

All three actions belong to `ship_state`. This flow needs no other MCP tool. It uses `gh` for filing.

| Action | Returns | Notes |
|---|---|---|
| `ship_state({action:"deferred_propose_followups"})` | `{openCount, groups, display}` | `groups` maps `high`, `medium` and `low` to the open items of that priority. `display` is markdown. It is empty when `openCount` is 0. |
| `ship_state({action:"deferred_list"})` | `{issues, openCount}` | `issues` holds every item, resolved ones too. There is no `display`. |
| `ship_state({action:"deferred_resolve", detail:{id:"<id>"}})` | `{ok, id}` | An id that is absent from the store is an error. Its suggestion points at `deferred_list`. An id that is already resolved succeeds and changes nothing, so check `status` before you report an item as newly resolved. |

An item has `id`, `created`, `source`, `priority`, `description` and `status`. It may also have `severity`, `file`, `line` and `reason`. It has no body field.

`source` names the tool that recorded the item, not the reason it was deferred. `ship_state defer` writes source `review-below-threshold` and an id `review-deferred-<timestamp>-<N>` for **every** record it makes — including `/received-review`'s `wont-fix`, `disagree` and `needs-direction` findings, which are not below any threshold. Read `reason` to tell those apart. The execute step writes source `execute-drift` and an id `execute-drift-<timestamp>-<N>`, with no `reason`. `deferred_add` takes a caller-supplied `source` and `id`, so neither is a closed set. The tool that finds an item records it at that moment. This flow never adds an item.

## The flow

### 1. Ask what to do

Ask one `AskUserQuestion`:

> {openCount} deferred issue(s) from previous runs still open.
>
> Options:
> 1. **Create GitHub issues** - file the items you pick as issues
> 2. **Resolve without filing** - mark the items you pick as resolved
> 3. **Skip** - leave everything open and review later

Option 3 ends the flow. Nothing changes. Every item stays open.

### 2. Pick the items

For option 1 or 2, ask which items to act on. Use one multi-select `AskUserQuestion` with one option per open item, labelled `[<id>] <description>`.

- If exactly one item is open, it is the pick. Do not ask.
- `AskUserQuestion` lists only a few options. If more items are open than it can list, print the ids in chat and ask the user to reply with the ids to act on, or with `all`.

### 3. Option 1: file each picked item

Take the picked items one at a time. For each item:

1. **Draft.** Build the issue title and body as described in "The issue text" below.
2. **Show.** Print the title and the complete body in chat, in a fenced block, exactly as they will be filed. A summary or a shortened version does not count.
3. **Get approval.** Ask one `AskUserQuestion`: file as shown, change the text, or skip this item. On "change the text", take the user's edits, show the full text again, and ask again. On "skip", leave the item open and go to the next item.
4. **File.** Only after approval, run `gh issue create` with the approved title, the approved body, and the label `deferred-followup`. Give `gh` the body from a file or from standard input, not inline on the command line, so quotes and backticks in the body cannot break the command.
5. **Resolve.** Only when `gh` succeeded, call `ship_state({action:"deferred_resolve", detail:{id:"<id>"}})`. Print the issue URL that `gh` returned.
6. **On failure.** If `gh` fails, print its error text. Do not call `deferred_resolve`. The item stays open. Then read the error and act:

| `gh issue create` failure | What to do |
|---|---|
| Auth, permission, or repo not found | Stop the batch. Every remaining item would fail the same way. Tell the user what to fix. |
| Network, 5xx, or rate limit | Retry this item once. If it fails again, stop the batch. |
| Ambiguous — a timeout or a killed command, where GitHub may already have created the issue | Do not file again blind. Run `gh issue list --label deferred-followup --search "<title>"` first. If it finds the issue, treat the filing as done: print its URL and go to step 5. If not, retry this item once. |
| Missing `deferred-followup` label | Handle the label as below, then retry this item. |
| Anything else | Leave the item open and go on to the next item. |

**The label.** `gh label create deferred-followup` fixes a missing label, but it writes to the shared remote GitHub repo: the label is visible to everyone and stays after this session. Ask the user before running it, the same way `gh issue create` is approved. Offer the other choice in the same question: file this issue with no label and add the label later. Never run `gh label create` on your own.

If `deferred_resolve` fails after the issue was created, say so, print the issue URL, and tell the user to run `/sdlc:deferred --resolve <id>`. Do not file the same issue a second time.

### 4. Option 2: resolve without filing

For each picked item, call `ship_state({action:"deferred_resolve", detail:{id:"<id>"}})`. Make no draft and no `gh` call. The pick in step 2 is the user's decision, so ask nothing more.

### 5. Close out

Print one line with three counts: items filed, items resolved without filing, and items still open.

## The issue text

**Title:** the item's `description`.

**Body:** built from the item's fields. Leave out any line whose field is empty.

```text
## Deferred follow-up

{description}

- Priority: {priority}
- Recorded: {created}
- Severity: {severity}
- Location: {file}:{line}
- Reason deferred: {reason}
- Deferred item id: {id}
```

`Location` drops `:{line}` when `line` is 0 or unset.

The body carries no `Source` line. `source` is `review-below-threshold` on every `ship_state defer` record, so printing it would contradict a `Reason deferred` of `disagree`, `needs-direction` or `wont-fix`. `Reason deferred` says why the item was deferred, and the `Deferred item id` prefix says which tool recorded it.

A caller that holds a fuller draft passes it in. Ship's execute-drift drafts carry their own `title` and `body`. Use those as the issue text instead of the template. They go through the same show and approve steps.

The store keeps only a title for an execute-drift item. When this flow runs later, outside the ship run that made the draft, the body is therefore short. That is expected. Do not invent detail the item does not have.

## Rules that hold in every case

- Render `display` verbatim. Never paraphrase or reformat it.
- Never run `gh issue create` before the user has approved the full text in chat. This holds even when the caller is otherwise unattended.
- Every issue gets the label `deferred-followup`, unless the user chose to file without it because the label does not exist yet.
- `gh label create` writes to the shared remote repo. Never run it without asking the user first.
- Never append an AI-tool attribution line ("Generated with Claude Code" or similar) to an issue body.
- Call `deferred_resolve` for an item only after its issue exists, or when the user chose to resolve it without filing.
- Never Read or edit the deferred store file by hand. Every change goes through `ship_state`.
