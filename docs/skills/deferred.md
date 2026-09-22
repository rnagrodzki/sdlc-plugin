# /deferred

Show the items that earlier pipeline runs recorded but did not fix, and triage
them: file an item as a GitHub issue, or resolve it without filing. The full
command name is `/sdlc:deferred`.

## When to use

- A `/ship` run ended and its summary said deferred items are still open.
- You want to see the backlog: review findings that fell below the review
  threshold, and issue drafts raised while a plan was executing.
- You want to turn a deferred item into a tracked GitHub issue.
- You decided an item is not worth tracking and want to close it.

## Syntax

    /deferred [--list | --resolve <id>]

With no flag, `/deferred` runs the triage flow.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--list` | Print the open items as a table and stop. Asks nothing and changes nothing. Cannot be combined with `--resolve`. | off |
| `--resolve <id>` | Mark the item with this id as resolved, without filing an issue. Asks nothing. Cannot be combined with `--list`. | — |

## Examples

**Triage the open items:**

    /deferred

Shows every open item grouped by priority, then asks whether to create GitHub
issues, resolve items without filing, or skip. You pick the items. For each
issue, the full title and body are shown in chat first, and nothing is filed
until you approve it. If no item is open, it prints `No deferred items open.`
and stops.

**Print the backlog only:**

    /deferred --list

Prints a table of the open items, high priority first. Resolved items are not
shown.

**Resolve one item without filing:**

    /deferred --resolve review-deferred-2026-09-21T10:15:00Z-1

Marks that one item resolved. Use `--list` first to find the id.

## Related skills

- [/ship](ship.md) — Its last step runs the same triage on the items still open
  when a run ends.
- [/review](review.md) — Findings below the review threshold are recorded as
  deferred items.
- [/execute](execute.md) — Issue drafts raised while a plan runs are recorded as
  deferred items.

## Tips and gotchas

- **Items are recorded when they are found.** They are stored in the project's
  deferred-items file as soon as a review or execute step creates them, so they
  survive an early exit from a pipeline run. `/deferred` only reads and closes
  them. It never adds one.
- **Nothing is filed without your approval.** Before each `gh issue create`,
  the complete title and body are printed in chat. You can approve, change the
  text, or skip that item. Every issue gets the label `deferred-followup`, and
  no AI-tool attribution line is added to the body.
- **An item is resolved only after its issue exists.** If `gh` fails, for
  example because the `deferred-followup` label does not exist yet, the item
  stays open. Create the label once with `gh label create deferred-followup`,
  then run `/deferred` again.
- **The issue body is as detailed as the recorded item.** Run from `/ship`'s
  last step, an execute draft is filed with its full body. Run later on its own,
  the body is built from the recorded fields: description, source, priority,
  time, and file, severity and reason when they were set.
- **Triage always asks.** `/deferred` has no automatic mode. `/ship` in its
  automatic mode skips this question, so items wait until you run `/deferred`.
- **Resolving cannot be undone from here.** There is no command to reopen a
  resolved item.
