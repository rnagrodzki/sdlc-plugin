# /preplan

Shape an idea into one topic file before you plan it. The skill asks you
questions one at a time, writes your answers into the topic file, and checks
each proposal against your plan guardrails. It works while plan mode is
active. At the end it can hand one or more ready topic files to
[`/plan`](plan.md), start a new idea, or stop. The full command name is
`/sdlc:preplan`.

## When to use

- You have an idea that is still vague, and you want to think it through
  before you write a plan.
- You want to find out early that a proposal breaks one of your plan
  guardrails, before a plan run finds it.
- You want a written record of the goal, the problem, the users, the risk,
  the non-goals, the flows, and the decisions that a plan will use as its
  requirements.
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

The skill changes the file only through `plan_support` tool calls, never
with a file edit. Plan mode permits these calls. The tool checks each
section body against the format rules. It refuses a body over 64 KiB
(65536 bytes) or a body that breaks a rule, and the error names the rule
(see [Format rules](#format-rules)).

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
sections in this order. When you run the skill again on an older file, the
tool adds each section heading that the file does not have. That step does not
add `Open questions`. The tool adds that section, after `Decisions`, when the
skill sends the first open question, and deletes it with the last one:

| Section | Content |
|---|---|
| `Goal` | One or two sentences: the result you want. |
| `Problem and benefit` | The root problem in one sentence, who gains, the measure with its baseline, and the cost of no change. |
| `Users and effect` | Who uses the change, what changes for them, and what fails today. |
| `Risk if wrong` | Who or what fails if the change has a defect. |
| `Non-goals` | What the change does not build, one bullet for each. A scope option that you did not pick is a bullet `Offered, not picked: <option>.` If you say that the change excludes nothing, the line `None stated.` |
| `Flows` | A before and after diagram for each flow that a proposal changes, with the same steps as a numbered list. If no flow changes, the line `No flow change.` |
| `Decisions` | A table with one row for each proposal. The number of a row never changes, and the skill never uses it again. Under the table, one line holds your answer to the cut question (see [How the dialogue works](#how-the-dialogue-works)), for example `Smallest cut: #1, #3.`, `Smallest cut: #2.` or `Smallest cut: all.`. The numbers are in ascending order. The line is not a row. |
| `Open questions` | One bullet for each question that has no answer yet. After the skill converts an older file, the section exists only when at least one question is open. |

After the first check, one `**Guardrail check:**` line under the Started line
shows the number of guardrails, the proposal counts and the UTC time of the
last check.

The file has a status line. It shows where the topic stands:

| Status | Meaning |
|---|---|
| `in progress` | The dialogue is running. This is the status of a new file. |
| `ready for plan` | You finished the dialogue. The file can go to `/plan`. |
| `paused` | You cancelled during the dialogue, at a guardrail question or at the empty-section question, or the guardrail check stopped with an error (see [Guardrail check](#guardrail-check)), or the tool refused the write of one answer, or of one guardrail check, three times, or a write of the topic file failed after the first question (see [How the dialogue works](#how-the-dialogue-works)). The skill keeps the file. |

The skill sets a status only at these events. You can also set any of the
three statuses on the Preplans tab of the dashboard, with no dialogue event
(see [Change a preplan status](../dashboard.md#change-a-preplan-status)).
For example, a topic can be `paused` with no cancel, or `ready for plan`
with no finished dialogue. The skill treats such a status the same as its
own.

The skill never replaces an existing file. When you run it again, the tool
adds each section heading that the file does not have. Each added section is
empty, so the skill asks for it in the dialogue. Run `/preplan` again with the
same topic to continue from the file. The status becomes `in progress`, and
the skill shows you the open questions, if any. An older file loses its
`Guardrail check` table and an empty `Open questions` section. The skill keeps
each kept or dropped result as a note in the `Decisions` table. A conversion
that stops half way is safe to repeat. Each conversion write removes the
condition that caused it, so the next run sends only the writes that are still
needed. The `Decisions` write skips a note that the `Reason` cell already
holds, so the cell never gets a second kept note (rule P6), and a repeat gives
the same file.

### Format rules

`preplan_write` checks the new body of one section before it writes. First it
changes each CR LF to LF and removes the final line ends. A body that breaks
a rule is refused, and the file does not change. The error names the section
and the rule, for example:

    preplan_write: Section "Decisions" breaks rule P5: row 2 has 2 cells, want 3

The dashboard uses the same check: a section that breaks a rule is unread
(see the `repos[].preplans[]` row of the
[Snapshot contract](../dashboard.md#snapshot-contract)).

| Rule | Section | Accepted form |
|---|---|---|
| P1 | all | The body is 65536 bytes or less. |
| P2 | all | No line outside a code fence starts with `# ` or `## `. |
| P3 | all | Each code fence closes. |
| P4 | `Decisions` | A body that is not blank starts with the two header lines `\| # \| Decision \| Reason \|` and `\|---\|---\|---\|`. |
| P5 | `Decisions` | Each row has 3 cells. The `#` value is a positive whole number, written with digits only, and no other row has it. |
| P6 | `Decisions` | A `Reason` cell holds one `Kept with warning:` note at most. A row whose `Decision` cell starts with `Dropped:` has `Dropped by:` in its `Reason` cell. |
| P7 | `Decisions` | After the table, only blank lines and at most one line `Smallest cut: all.` or `Smallest cut: #1, #3.`. The numbers are in ascending order, and each one is a row that is not dropped or moved. |
| P8 | `Non-goals` | The body is `None stated.`, or each line that is not blank starts with `- `. |
| P9 | `Open questions` | Each line that is not blank starts with `- `. |
| P10 | `Flows` | The body is `No flow change.`, or it holds a `mermaid` code fence. Each `classDef` line is one of the two `classDef` lines that the skill copies into each diagram (`classDef new ...` and `classDef changed ...`), with no change except leading spaces. |
| P11 | `Guardrail check` | Only an empty body. It deletes this older section. A body of spaces is not empty. |

A section with no rule of its own, for example `Goal`, has rules P1 to P3
only.

### How the dialogue works

- The skill asks one question at a time. It asks about function and effect:
  who uses it, what changes, and what fails. It does not ask about code.
- Before each question, the skill restates the facts that you need to answer.
- After each answer, the skill sends each changed section to the tool before
  the next question.
- When the tool refuses a write, the skill fixes the section and sends it
  again. The count is per answer, or per guardrail check: on the third
  refusal for one answer or one guardrail check, the skill stops trying. The
  count starts at 0 again after a write that succeeds.
- After the topic file exists, the skill sets the status `paused` and ends
  when you cancel, when the tool refuses the write of one answer, or of
  one guardrail check, three times, or when a tool call fails after the
  first question. Run
  `/preplan <topic>` to continue. If the `paused` write itself fails, the
  skill prints the error and the status stays as it was.
- The skill asks for the sections in this order:
  1. `Goal`
  2. `Problem and benefit`
  3. `Users and effect`
  4. `Risk if wrong`
  5. `Non-goals`

  Then it asks the open questions, then a missing `Flows` entry, then the next
  proposal. It starts at the first section that has a gap.
- If an answer is vague, the skill asks one follow-up question.
- If a benefit has no measure or no baseline, the skill asks one follow-up
  question for both.
- If a question offers scope options and you pick some, the skill adds each
  option that you did not pick to `Non-goals`, as a bullet in this form:
  `Offered, not picked: <option>.` It does not record the "done" option or
  the "cancel" option.
- If you say that the change excludes nothing, the skill writes the line
  `None stated.` under `Non-goals`. The skill deletes that line when the
  section gets a bullet.
- After each answer, the skill updates the topic file, then asks the next
  question.
- Each question has a "done" option and a "cancel" option.
- The cut question: when you answer "done" and the file has two or more
  proposals that are not dropped or moved, and no `Smallest cut:` line, the
  skill asks which proposals form the smallest version that still gives
  value. Answer `all`, or a list of proposal numbers. "done" means `all`.
  The skill writes your answer as the `Smallest cut:` line under the
  `Decisions` table. The cut question is not a scope question, so it adds no
  `Non-goals` bullet. If your answer is neither `all` nor the number of a
  proposal that is not dropped or moved, the skill names the valid numbers
  and asks again. With one such proposal, or with a `Smallest cut:` line
  already in the file, the skill does not ask the cut question. When you add
  a new proposal, the skill deletes a `Smallest cut:` line that lists
  numbers, because that cut question did not offer the new proposal, and it
  asks the cut question again. A `Smallest cut: all.` line stays.

### Guardrail check

Each proposal is checked against the plan guardrails before you see it as
accepted. Set the guardrails with `/setup --guardrails`. Each guardrail has a
severity: `error`, or `warning`. A guardrail with no severity counts as
`error`.

Each check looks at every proposal again, except a dropped or moved one. After the check, one line under the
Started line records it:

    **Guardrail check:** 53 guardrails, 7 proposals: 6 pass, 1 kept, 0 dropped. Last check: 2026-10-21T17:05:00Z.

If the skill could not read the guardrails, the line ends with
`Guardrails unavailable: <reason>.` A proposal with a kept note counts as kept. A dropped proposal counts as dropped. A moved proposal counts nowhere. Each other proposal counts as a pass.

A tool error can stop the skill before the first question or during the
dialogue. This section lists the errors of the first step and of the check.
A write of a section or of the status can fail too (see
[How the dialogue works](#how-the-dialogue-works)).

The first step of the skill loads the guardrails and the topic file. An error
there stops the skill before the first question. The skill prints the error and
stops. The status does not change, and the skill writes no `Guardrail check`
line. The first step stops in these cases:

- The topic is not valid (see [Flags](#flags)).
- Two files match the topic, or the folder holds more than 10000 names (see
  [Topic file](#topic-file)).
- The folder cannot be listed, or the tool cannot create the topic file. Fix
  the folder or its permission as the printed error says, then run the skill
  again.
- The topic file is a link, a folder, or another file that is not a regular
  file. The first step refuses a link too, even a link to a regular file.
  Replace the link with a copy of the file that it points to, then run the
  skill again.
- The topic file is larger than 8 MiB, or the added section headings would
  make it larger than 8 MiB. The tool refuses a larger file before it reads
  it, and it changes nothing. Make the file smaller, then run the skill again.
- A read of the topic file fails, or the write of the added section headings
  fails. The file stays as it was. Fix the cause that the printed error names,
  for example the file or folder permission, then run the skill again.

The check can stop the dialogue with an error. The skill prints the error,
then goes to the cancel step, which sets the status to `paused`. An error at
the first step, or at the status write or the conversion of an older file
before the first question, does not set `paused`. A write error after a question also goes to the cancel step.
The check stops in these cases:

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

Two results also leave a short note at the end of the `Reason` cell in the
`Decisions` table:

| Note | Meaning |
|---|---|
| `Kept with warning: <ids>.` | The proposal breaks only `warning` guardrails, and you chose to keep it. A later check does not ask again while the note lists every `warning` guardrail that the proposal breaks. If you change the proposal, or it breaks a new `warning` guardrail, the skill asks again. |
| `Dropped by: <ids>.` | The proposal breaks an `error` guardrail, and it still did after one rework. |

What the skill does when a proposal breaks a guardrail:

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

If you change a proposal later, the skill checks it again.

After the check, the skill removes from the `Smallest cut:` line the number of
each proposal that is dropped or moved. If this removes the last number, the
skill deletes the line. If two or more proposals remain, the skill then asks
the cut question again before the handoff. A `Smallest cut: all.` line stays
as it is.

### Hand off to plan

When you answer "done", the skill needs at least one proposal that is not
dropped, and either a flow diagram or the line `No flow change.` under
`Flows`. If one is missing, the skill tells you what is missing and keeps
asking. If the file has two or more proposals that are not dropped or moved,
and no `Smallest cut:` line, the skill asks the cut question first (see
[How the dialogue works](#how-the-dialogue-works)).

Then the guardrail check runs. If a section is still empty after the check,
the skill lists the empty sections and asks: fill them, or go to the handoff
with the empty sections. It asks on each final check that still finds an
empty section. A section is empty when it is absent or
holds only blank lines. Only the seven sections from `Goal` to `Decisions` count:
`Open questions` is never an empty section. A `Decisions` table with no row is empty too. The
skill says that `/plan` can start with the empty sections, but each one is a
gap in its requirements. The "done" option means go to the handoff. The skill
does not record your answer in the file, so a later "done" with an empty
section asks again.

- **Fill them:** the skill goes back to the dialogue. The first gap is the
  first empty section. If a section heading is missing from the file, for
  example because you deleted it, the tool adds it again first.
- **Go to the handoff:** the skill continues as below.
- **Cancel:** the status becomes `paused` and the skill keeps the file.
- **Any other answer**, for example free text: the skill lists the empty
  sections again and asks again.

When the file is complete, or you chose to go to the handoff, the status
becomes `ready for plan`. If the `ready for plan` write fails, the skill
prints the error and tries to set `paused`. Then it ends and asks no handoff
question. Run `/preplan <topic>` again after the fix. When the write
succeeds, the skill asks you to choose:

- **Start plan now:** the skill runs `/sdlc:plan` in the same session. The
  plan skill reads the topic file as its requirements file. If other topic
  files are `ready for plan`, the skill first asks which of them to add. It
  always includes this file. It offers up to 3 other ready files to add, the
  newest first. It looks
  only at the 100 topic files with the latest change, and it names the
  number of other ready files that do not fit in the list. If you pick no
  file, the plan starts with this topic file only. If the tool cannot list
  the ready files, the skill says so, and the plan starts with this topic
  file only.
- **Start a new idea:** the skill asks the name of the next topic, then runs
  `/preplan <topic>` in the same session. This file stays `ready for plan`,
  unless the topic name matches this file; the new run then sets it to
  `in progress`. If you cancel at the topic question, the skill ends, and
  this file stays `ready for plan`.
- **Stop:** the skill ends. The status stays `ready for plan`.
- **Any other answer**, for example free text: the skill restates the three
  options and asks again.

If the plan skill stops with an error, the skill prints the error. The
topic file stays `ready for plan`. Run `/plan` with the same topic-file
paths to try again.

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
2026-10-13 continues this file. The skill keeps it. The tool adds each section
heading that the file does not have. The skill converts an older file to the
new form and lists its open questions, if any. Then the dialogue goes on from
there.

**Plan from a topic file without the handoff:**

    /plan .sdlc-v2/preplan/2026-10-12-auth-flow.md

Use this after you chose "Stop" at the handoff, or when the plan skill
stopped with an error.
