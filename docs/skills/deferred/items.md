# Deferred items

Read this page when you want to know where an item of the backlog comes from and what its fields mean. It also explains why an expected item can be absent from the list.

Triage prints one line for each open item, under its priority (`High`, `Medium` or `Low`):

```text
- [<id>] <first line of the description> (source: <source>)
```

The [overview](../deferred.md#example) shows the command.

## Where an item comes from

The step that finds an item records it at that moment. `/sdlc:deferred` reads and closes items. It never adds one.

| Recorded by | `source` | `id` |
|---|---|---|
| The review step of `/sdlc:ship`, for a finding below the review threshold | `review-below-threshold` | `review-deferred-<timestamp>-<N>` |
| `/sdlc:received-review`, for a finding that it does not fix | `received-review` | `review-deferred-<timestamp>-<N>` |
| The execute step, for an issue draft raised while a plan runs | `execute-drift` | `execute-drift-<timestamp>-<N>` |
| `/sdlc:harden --auto`, for rule edits that need a person | `harden-pending` | such as `harden-pending-plan` or `harden-pending-plan-2` |

- `/sdlc:harden --auto` stores one item for each rule section. Its description starts with the line `Pending rule edits: <section>`.
- This table is not a closed set. The recording tools take the source from their caller, so other sources can appear.

## The fields of an item

An item has `id`, `created`, `source`, `priority`, `description` and `status`. It can also have `severity`, `file`, `line` and `reason`.

| Field | What it holds |
|---|---|
| `priority` | `high`, `medium` or `low`. Triage groups the open items by this field. |
| `status` | `open` until the item is resolved. The lists show open items only. |
| `description` | The text of the item. It can have more than one line. The lists and the issue title use the first line only. |
| `reason` | Why the item was deferred: `below-threshold`, `needs-direction`, `disagree` or `wont-fix`. An execute draft has no reason. |
| `file`, `line`, `severity` | The location and the severity of a finding, when the recording step set them. |

## `source` is not the reason

`source` names the tool that recorded the item. It does not say why the item was deferred.

- Each item recorded through `ship_state defer` has an id that starts with `review-deferred-`.
- Its source is the value that the caller passed. `/sdlc:received-review` passes `received-review`. The source is `review-below-threshold` when the caller passes none.
- Read `reason` to know why an item was deferred. A `received-review` item with the reason `disagree` was never below a threshold.
- An execute draft has the source `execute-drift` and no reason.

## What `--list` prints

`/sdlc:deferred --list` prints a table with the columns `ID`, `Priority`, `Reason`, `Created` and `Description`.

- **Order:** high priority first, and the oldest item first within one priority.
- **Reason:** the `reason` of the item. When the item has no reason, the column shows its `source`.
- **Description:** the first line of the description only. When the item has a file, ` (file:line)` follows that line.
- **Resolved items:** not in the table.
- **No open item:** the skill prints `No deferred items open.` and no table.

## Limits

- **Items are recorded when they are found.** A review, execute, received-review or harden step writes each item to the project file `.sdlc-v2/history/deferred.json` at once. An item thus normally survives an early exit from a pipeline run.
- **The write to that file is best-effort.** When the write fails, the recording tool prints a warning and continues. That item is only in the state file of the run. It is lost when that state file is cleaned up, and it never reaches `/sdlc:deferred`. Look for those warnings during a run.
- **One `needs-direction` item for each file.** A new `needs-direction` finding on a file that already has an open `needs-direction` item adds no item. Its description becomes a new line of the open item.
- **Resolving cannot be undone from here.** No command of this skill opens a resolved item again.
- **An unknown id is an error.** `/sdlc:deferred --resolve <id>` with an id that is not in the backlog prints the error of the tool. Run `/sdlc:deferred --list` to see the open ids.
