# Guardrail check

Read this page when the skill reworked or dropped a proposal, asked you to
keep one with a warning, or stopped the check with an error.
`/sdlc:preplan` checks each proposal against your plan guardrails before you
see it as accepted.

## Example

After each check, one line under the Started line of the
[topic file](./topic-file.md) records the result:

```text
**Guardrail check:** 53 guardrails, 7 proposals: 6 pass, 1 kept, 0 dropped. Last check: 2026-10-21T17:05:00Z.
```

A proposal with a kept note counts as kept. A dropped proposal counts as
dropped. A moved proposal counts nowhere. Each other proposal counts as a
pass. If the skill could not read the guardrails, the line ends with
`Guardrails unavailable: <reason>.`

## What the check covers

Set the guardrails with [`/sdlc:setup --guardrails`](../setup.md). Each
guardrail has a severity: `error`, or `warning`. A guardrail with no severity
counts as `error`.

Each check looks at every proposal again, except a dropped or moved one. If
you change a proposal later, the skill checks it again.

## When a proposal breaks a guardrail

- **An `error` guardrail:** the skill reworks the proposal once and checks it
  again. If it still breaks an `error` guardrail, the skill drops it. The
  row stays in the `Decisions` table, marked `Dropped:`, so you can see why.
- **A `warning` guardrail only:** the skill shows you each guardrail and its
  rule, then asks once: keep the proposal, rework it, or cancel. If you rework
  it, the proposal moves to `Open questions` until you answer. Its row stays
  in the `Decisions` table, marked `Moved:`. When the
  proposal comes back, it gets a new row with a new number. If you cancel at
  this question, the skill writes none of the notes of this check: the next
  run checks every proposal again.

## Notes in the Decisions table

Two results leave a short note at the end of the `Reason` cell in the
`Decisions` table:

| Note | Meaning |
|---|---|
| `Kept with warning: <ids>.` | The proposal breaks only `warning` guardrails, and you chose to keep it. A later check does not ask again while the note lists every `warning` guardrail that the proposal breaks. If you change the proposal, or it breaks a new `warning` guardrail, the skill asks again. |
| `Dropped by: <ids>.` | The proposal breaks an `error` guardrail, and it still did after one rework. |

## Effect on the Smallest cut line

After the check, the skill removes from the `Smallest cut:` line the number of
each proposal that is dropped or moved. If this removes the last number, the
skill deletes the line. If two or more proposals remain, the skill then asks
the cut question again before the handoff (see
[The cut question](./dialogue.md#the-cut-question)). A `Smallest cut: all.`
line stays as it is.

## Errors that stop the check

The check can stop the dialogue with an error. The skill prints the error,
then goes to the cancel step, which sets the status to `paused`. Run
`/sdlc:preplan <topic>` again after the fix. The check stops in these cases:

- The tool finds no topic file, or two files match the topic.
- The folder cannot be listed, or it holds more than 10000 names.
- The topic file is a link, a folder, or another file that is not a regular
  file. The first step refuses these too, so the check meets one only when
  the file changed during the dialogue.
- The topic file is larger than 8 MiB. The limit and the refusal are the same
  as at the first step.
- The first 4096 bytes of the topic file hold no `**Started:**` line and no
  `**Status:**` line.
- A read or a write of the topic file fails.

An error at the first step does not set `paused` (see
[Errors at the start](./topic-file.md#errors-at-the-start)). A write of a
section or of the status can fail too (see
[When a write fails](./dialogue.md#when-a-write-fails)).
