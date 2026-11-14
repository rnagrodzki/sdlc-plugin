# /sdlc:deferred

See what earlier pipeline runs recorded but did not fix, and turn each item into a GitHub issue or close it.

## Why use it

A pipeline run records the items that it finds and does not fix. These deferred items go into one backlog of the project.
The items are review findings below the review threshold, issue drafts from the execute step, and findings that `/sdlc:received-review` left unfixed. Rule edits that `/sdlc:harden --auto` held back for a person are items too.
This skill shows the open items. You file an item as a GitHub issue, or you resolve it without an issue.
The skill files no issue before you approve its full text.

## Example

A `/sdlc:ship` run ended, and its summary says that deferred items are still open. Run:

    /deferred

The skill prints each open item under its priority, as `[<id>] <first line of the description> (source: <source>)`. Then it asks what to do:

- **Create GitHub issues**: file the items you pick as issues.
- **Resolve without filing**: mark the items you pick as resolved.
- **Skip**: leave everything open and review later.

For each issue, the skill shows the full title and body in chat. It files the issue only after you approve the text, then it prints the issue URL.

`/deferred` is the short name. The full command name is `/sdlc:deferred`.

## What you get

- See the whole backlog: each open item from earlier runs in one list, grouped by priority.
- Turn an item into a tracked GitHub issue: the skill drafts the title and the body from the recorded item and adds the label `deferred-followup`.
- Approve each issue first: you see the full text, then you file it as shown, change the text, or skip the item.
- Close an item that is not worth an issue: `--resolve <id>` resolves it and asks nothing.
- Look at no risk: `--list` prints the open items, asks nothing and changes nothing.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 1: Parse arguments | skill | Reads the flags and picks the mode: triage, list, or resolve one item. It stops with a usage line when both flags are given, when `--resolve` has no id, or when another argument appears. | The chosen mode | no | always |
| Step 2a: List the open items | skill | Reads the items through the `ship_state` tool and prints the open items as a table, high priority first. It asks nothing and changes nothing. | The table of open items | yes | `--list` |
| Step 2b: Resolve one item | skill | Marks the item with this id as resolved through the `ship_state` tool, and files no issue. It asks nothing. An item that is already resolved stays as it is. | The resolved item | yes | `--resolve <id>` |
| Step 2c: Triage | skill | Shows the open items and asks what to do. For each item you pick, it files a GitHub issue after you approve the full draft, or it resolves the item without an issue. | The filed issues, the resolved items and one line of counts | yes | no flag |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:deferred`: with no flag, with `--list`, or with `--resolve <id>`. |
| Ends | After Step 2 runs its mode. Triage and `--list` end at once when no item is open, and print `No deferred items open.` |
| Asks the user | In triage mode only. It always asks before it acts and shows the full issue draft first. It has no auto mode. `--list` and `--resolve` ask nothing. |
| Resume | none |

## More examples

**Print the backlog only:**

    /deferred --list

The skill prints a table of the open items, high priority first, with the columns `ID`, `Priority`, `Reason`, `Created` and `Description`. Resolved items are not in the table.

**Resolve one item without an issue:**

    /deferred --resolve review-deferred-2026-09-21T10:15:00Z-1

The skill prints `Resolved <id> without filing an issue.` Use `--list` first to find the id. For an item that is already resolved, it prints `<id> is already resolved.` and changes nothing.

**Pending rule edits are not filed.** For an item with the source `harden-pending`, triage files no issue and prints two lines:

```text
<id> holds rule edits that need a person. Run: /sdlc:harden --failure-text "Pending rule edits: <id>" --skill deferred
Then resolve it: /sdlc:deferred --resolve <id>
```

The first command applies the rule edits with your approval. The item stays open until you run the second command.
The choice to resolve without filing also leaves such an item open and prints the same command.

## Reference

- [Deferred items](./deferred/items.md): where an item comes from, what `source` and `reason` mean, and the limits. **The durable write is best-effort.** An item whose write failed is not in the backlog.
- [Triage and issue filing](./deferred/triage.md): the questions, the issue text, the label `deferred-followup`, and what the skill does when `gh` fails.

## Related skills

- [/sdlc:ship](./ship.md): runs the same triage in its last step. In its automatic mode it skips the question, so the items wait for this skill.
- [/sdlc:review](./review.md): in a `/sdlc:ship` run, its findings below the review threshold are recorded as deferred items.
- [/sdlc:execute](./execute.md): issue drafts raised while a plan runs are recorded as deferred items.
- [/sdlc:received-review](./received-review.md): records the findings that it does not fix, with a reason of `wont-fix`, `disagree` or `needs-direction`.
- [/sdlc:harden](./harden.md): under `--auto`, stores the rule edits that need a person as deferred items. Triage prints the command that applies them.
