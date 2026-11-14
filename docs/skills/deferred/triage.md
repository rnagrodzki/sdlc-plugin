# Triage and issue filing

Read this page when you file deferred items as GitHub issues. It tells you what the skill asks, what an issue holds, and what the skill does when `gh` fails.

Triage starts with `/sdlc:deferred` and no flag. The [overview](../deferred.md#example) shows the command and the first question.

## The questions

1. **What to do.** `Create GitHub issues`, `Resolve without filing` or `Skip`. `Skip` ends the triage. Nothing changes, and each item stays open.
2. **Which items.** A list with one option for each open item, labelled `[<id>] <first line of description>`. You can pick more than one.
   - When exactly one item is open, that item is the pick. The skill does not ask.
   - When more items are open than the list can show, the skill prints the ids. Reply with the ids, or with `all`.
3. **For each issue: approve the text.** The skill prints the title and the complete body, exactly as it will file them. You choose: file as shown, change the text, or skip this item.
   - After a change, the skill shows the full text again and asks again.
   - A skipped item stays open.

`Resolve without filing` asks nothing after question 2. Your pick is the decision.

Triage always asks. It has no auto mode. At the end, the skill prints one line with three counts: items filed, items resolved without filing, and items still open.

## The issue text

**Title:** the first line of the description of the item.

**Body:** built from the recorded fields. A line whose field is empty is left out.

```text
## Deferred follow-up

{fence}text
{description}
{fence}

- Priority: {priority}
- Recorded: {created}
- Severity: {severity}
- Location: {file}:{line}
- Reason deferred: {reason}
- Deferred item id: {id}
```

- **The description is in a code fence.** `{fence}` is a run of at least three backticks, one longer than the longest run of backticks in the description. The text of the description thus cannot pass for the metadata lines below it.
- **`Location` drops `:{line}`** when the item has no line.
- **The body has no `Source` line.** [`source` is not the reason](./items.md#source-is-not-the-reason). `Reason deferred` says why the item was deferred, and the prefix of the item id shows which step recorded it.
- **No AI-tool attribution line** is added to the body.
- **An issue is as detailed as the recorded item.** In the last step of `/sdlc:ship`, an execute draft is filed with its own full title and body. In a later run of `/sdlc:deferred`, the backlog holds only the title of that draft, so the body is short.

## The label `deferred-followup`

Each issue gets the label `deferred-followup`. When the label does not exist in the repository, `gh issue create` fails.

`gh label create deferred-followup` corrects this, but it writes to the shared remote repository. The skill thus asks you first. In the same question you can choose to file the issue with no label and add the label later.

## When `gh` fails

An item is resolved only after its issue exists. When `gh issue create` fails, the skill prints the error and the item stays open.

| Failure | What the skill does |
|---|---|
| Authentication, permission, or repository not found | Stops the batch, because each remaining item would fail the same way. It tells you what to correct. |
| Network, 5xx or rate limit | Tries this item one more time. A second failure stops the batch. |
| Timeout or a killed command, where GitHub possibly created the issue | Searches the issues with the label `deferred-followup` for the title first. A found issue counts as filed. If none is found, it tries one more time. |
| Missing `deferred-followup` label | Asks you about the label (see above), then tries this item again. |
| Any other failure | Leaves the item open and continues with the next item. |

Sometimes the issue exists but the skill cannot resolve the item. Then the skill prints the issue URL and tells you to run `/sdlc:deferred --resolve <id>`. It does not file the same issue again.

## Pending rule edits

An item with the source `harden-pending` holds rule edits that `/sdlc:harden --auto` stored for a person. Triage never files such an item, and `Resolve without filing` does not close it, because that would lose the rule edits. For both choices the skill prints the `/sdlc:harden` command that applies the edits and the `--resolve` command that closes the item afterward. See [Pending rule edits are not filed](../deferred.md#more-examples).

## The same triage in `/sdlc:ship`

The last step of `/sdlc:ship` runs this triage on the items that are still open when a run ends. Two things differ:

| | `/sdlc:deferred` | `/sdlc:ship` |
|---|---|---|
| No item is open | Prints `No deferred items open.` and stops. | Prints nothing and continues. |
| The questions | Always asked. | Skipped in the automatic mode of ship. Ship still prints the open items, and they wait for `/sdlc:deferred`. |

## For contributors

The triage flow is written in exactly one place: `plugins/sdlc/skills/deferred/reference.md`. Two callers follow it: `/sdlc:deferred` in its default mode, and `/sdlc:ship` Step 10b. Change the flow in that file. Do not copy it into a caller.
