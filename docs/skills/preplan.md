# /preplan

Shape an idea into one topic file before you plan it. The skill asks you
questions one at a time, writes your answers into the topic file, and checks
each proposal against your plan guardrails. At the end it can hand the topic
file to [`/plan`](plan.md). The full command name is `/sdlc:preplan`.

## When to use

- You have an idea that is still vague, and you want to think it through
  before you write a plan.
- You want to find out early that a proposal breaks one of your plan
  guardrails, before a plan run finds it.
- You want a written record of the goal, the users, the flows, and the
  decisions that a plan will use as its requirements.
- You stopped half way and want to continue the same topic later.

`/preplan` starts no plan run and writes no plan. Use [`/plan`](plan.md)
when your requirements are already clear.

## Syntax

    /preplan [topic]

With no topic, the skill asks you for one.

## Flags

| Flag | Meaning |
|---|---|
| `[topic]` | short topic name. It becomes part of the topic file name |

The topic must be on one line. It must have 1 to 50 characters and at least
one ASCII letter or digit. If the topic is not valid, the skill prints the
error and stops. It creates no file.

## Topic file

The skill keeps all its state in one Markdown file:
`.sdlc-v2/preplan/<date>-<name>.md`. `<date>` is the UTC date of the first
run, as `YYYY-MM-DD`. The name comes from the topic in four steps:

1. The skill changes the topic to lowercase.
2. The skill keeps only the ASCII letters `a-z` and the digits `0-9`. It
   changes every other character to a hyphen. This includes letters that are
   not ASCII, such as `é`.
3. Each run of hyphens becomes one hyphen.
4. The skill removes the hyphens at the start and at the end.

On 2026-10-12 the topic `Auth flow` gives `.sdlc-v2/preplan/2026-10-12-auth-flow.md`.
The topic `Café login` gives `.sdlc-v2/preplan/2026-10-12-caf-login.md`.

A later run with the same topic continues the file named exactly
`<name>.md` or `YYYY-MM-DD-<name>.md`. A file with no date, from before
this rule, keeps its name. If two files match, the skill stops and names
them. Rename or delete one of them, then run the skill again. If the
folder holds more than 10000 names, the skill stops. Move old topic files
out of the folder, then run the skill again.

A new topic file has a `**Started:**` line after the `**Status:**` line.
It holds the UTC time of the first run in RFC 3339 form, for example
`**Started:** 2026-10-12T21:30:00Z`. The dashboard shows the time from
this line to the last change of the file as the duration. A file with no
Started line shows a dash.

The topic file is in the main worktree, so every worktree of the repo sees
the same file.

The skill asks the `plan_support` tool to create the file once, with these
sections in this order:

| Section | Content |
|---|---|
| `Goal` | One or two sentences: the result you want. |
| `Users and effect` | Who uses the change, what changes for them, and what fails today. |
| `Flows` | A before and after diagram for each flow that a proposal changes, with the same steps as a numbered list. If no flow changes, the line `No flow change.` |
| `Decisions` | A table with one row for each proposal. The number of a row never changes, and the skill never uses it again. |
| `Open questions` | One bullet for each question that has no answer yet. |
| `Guardrail check` | A table with one result row for each proposal. |

The file has a status line. It shows where the topic stands:

| Status | Meaning |
|---|---|
| `in progress` | The dialogue is running. This is the status of a new file. |
| `ready for plan` | You finished the dialogue. The file can go to `/plan`. |
| `paused` | You cancelled. The skill keeps the file. |

The skill sets a status only at these events. You can also set any of the
three statuses on the Preplans tab of the dashboard, with no dialogue event
(see [Change a preplan status](../dashboard.md#change-a-preplan-status)).
For example, a topic can be `paused` with no cancel, or `ready for plan`
with no finished dialogue. The skill treats such a status the same as its
own.

The skill never overwrites an existing file. Run `/preplan` again with the
same topic to continue from the file. The status becomes `in progress`, and
the skill shows you the open questions.

### How the dialogue works

- The skill asks one question at a time. It asks about function and effect:
  who uses it, what changes, and what fails. It does not ask about code.
- Before each question, the skill restates the facts that you need to answer.
- If an answer is vague, the skill asks one follow-up question.
- After each answer, the skill updates the topic file, then asks the next
  question.
- Each question has a "done" option and a "cancel" option.

### Guardrail check

Each proposal is checked against the plan guardrails before you see it as
accepted. Set the guardrails with `/setup --guardrails`. Each guardrail has a
severity: `error`, or `warning`. A guardrail with no severity counts as
`error`.

Each proposal gets one row in the `Guardrail check` table. The `Result`
column shows what happened:

| Result | Meaning |
|---|---|
| `pass — <basis>` | The proposal breaks no guardrail. The `Guardrail` column lists each guardrail that the skill checked. `<basis>` gives, for each guardrail, the fact of the proposal that clears it. When no guardrail is configured, the result is `pass — no guardrail configured`. |
| `kept — <ids>` | The proposal breaks only `warning` guardrails, and you chose to keep it. |
| `dropped — <ids>` | The proposal breaks an `error` guardrail, and it still did after one rework. |
| `unavailable — <warning>` | The skill could not read the guardrails. The proposal counts as a pass. |

What the skill does when a proposal breaks a guardrail:

- **An `error` guardrail:** the skill reworks the proposal once and checks it
  again. If it still breaks an `error` guardrail, the skill drops it. The
  row stays in the `Decisions` table, marked `Dropped:`, so you can see why.
- **A `warning` guardrail only:** the skill shows you each guardrail and its
  rule, then asks once: keep the proposal, rework it, or cancel. If you rework
  it, the proposal moves to `Open questions` and has no check row until you
  answer. Its row stays in the `Decisions` table, marked `Moved:`. When the
  proposal comes back, it gets a new row with a new number.

If you change a proposal later, the skill checks it again.

### Hand off to plan

When you answer "done", the skill needs at least one proposal that is not
dropped, and either a flow diagram or the line `No flow change.` under
`Flows`. If one is missing, the skill tells you what is missing and keeps
asking.

When the file is complete, the status becomes `ready for plan`. The skill
then asks you to choose:

- **Start plan now:** the skill runs `/sdlc:plan` in the same session. The
  plan skill reads the topic file as its requirements file.
- **Stop:** the skill ends. The status stays `ready for plan`.

If the plan skill stops with an error, the skill prints the error. The
topic file stays `ready for plan`. Run `/plan <topic-file-path>` to try
again.

## Examples

**Start a topic:**

    /preplan auth flow

Creates `.sdlc-v2/preplan/2026-10-12-auth-flow.md` (on 2026-10-12, UTC) and asks the first question. Each
answer goes into the file before the next question.

**Start without a topic:**

    /preplan

The skill asks for a short topic name first. If you cancel at this point, no
file is created.

**Continue a topic later:**

    /preplan auth flow

The file `2026-10-12-auth-flow.md` already exists. It was created on an
earlier day: the date in the name is the day of the first run. A run on
2026-10-13 continues this file. The skill keeps it as it is and lists its
open questions. Then the dialogue goes on from there.

**Plan from a topic file without the handoff:**

    /plan .sdlc-v2/preplan/2026-10-12-auth-flow.md

Use this after you chose "Stop" at the handoff, or when the plan skill
stopped with an error.
