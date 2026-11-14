# Dialogue and handoff

Read this page when you want to know which question comes next, what "done"
and "cancel" do, or how a finished topic goes to
[`/sdlc:plan`](../plan.md).

## Example

A run of `/sdlc:preplan auth flow` on a new topic goes like this:

1. The skill asks about the goal, then the problem and benefit, the users and
   effect, the risk and the non-goals. It asks one question at a time.
2. The skill writes each answer into the [topic file](./topic-file.md) before
   the next question.
3. You describe a proposal. The skill checks it against the plan guardrails
   (see [Guardrail check](./guardrail-check.md)).
4. You answer "done". With two or more proposals, the skill asks
   [the cut question](#the-cut-question) first.
5. The final check passes. The status becomes `ready for plan`, and you
   choose: start `/sdlc:plan` now, start a new idea, or stop.

## How the dialogue works

- The skill asks one question at a time. It asks about function and effect:
  who uses it, what changes, and what fails. It does not ask about code.
- Before each question, the skill restates the facts that you need to answer.
- Each question has a "done" option and a "cancel" option.
- After each answer, the skill sends each changed section to the tool, then
  asks the next question.
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

## The cut question

When you answer "done" and the file has two or more proposals that are not
dropped or moved, and no `Smallest cut:` line, the skill asks which proposals
form the smallest version that still gives value.

- Answer `all`, or a list of proposal numbers. "done" means `all`.
- The skill writes your answer as the `Smallest cut:` line under the
  `Decisions` table, for example `Smallest cut: #1, #3.`
- The cut question is not a scope question, so it adds no `Non-goals` bullet.
- If your answer is neither `all` nor the number of a proposal that is not
  dropped or moved, the skill names the valid numbers and asks again.
- With one such proposal, or with a `Smallest cut:` line already in the file,
  the skill does not ask the cut question.
- When you add a new proposal, the skill deletes a `Smallest cut:` line that
  lists numbers, because that cut question did not offer the new proposal,
  and it asks the cut question again. A `Smallest cut: all.` line stays.

The guardrail check can also change the line (see
[Effect on the Smallest cut line](./guardrail-check.md#effect-on-the-smallest-cut-line)).

## When a write fails

- When the tool refuses a write, the skill fixes the section and sends it
  again. The count is per answer, or per guardrail check: on the third
  refusal for one answer or one guardrail check, the skill stops trying. The
  count starts at 0 again after a write that succeeds. The rules that the
  tool checks are the [Format rules](./topic-file.md#format-rules).
- After the topic file exists, the skill sets the status `paused` and ends
  when you cancel, when the tool refuses the write of one answer, or of
  one guardrail check, three times, or when a tool call fails after the
  first question. Run `/sdlc:preplan <topic>` to continue.
- If the `paused` write itself fails, the skill prints the error and the
  status stays as it was.

## Hand off to plan

When you answer "done", the skill needs at least one proposal that is not
dropped, and either a flow diagram or the line `No flow change.` under
`Flows`. If one is missing, the skill tells you what is missing and keeps
asking. If the file has two or more proposals that are not dropped or moved,
and no `Smallest cut:` line, the skill asks the cut question first (see
[The cut question](#the-cut-question)).

Then the guardrail check runs. If a section is still empty after the check,
the skill lists the empty sections and asks: fill them, or go to the handoff
with the empty sections. It asks on each final check that still finds an
empty section. A section is empty when it is absent or
holds only blank lines. Only the seven sections from `Goal` to `Decisions` count:
`Open questions` is never an empty section. A `Decisions` table with no row is empty too. The
skill says that `/sdlc:plan` can start with the empty sections, but each one is a
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
question. Run `/sdlc:preplan <topic>` again after the fix. When the write
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
  `/sdlc:preplan <topic>` in the same session. This file stays `ready for plan`,
  unless the topic name matches this file; the new run then sets it to
  `in progress`. If you cancel at the topic question, the skill ends, and
  this file stays `ready for plan`.
- **Stop:** the skill ends. The status stays `ready for plan`.
- **Any other answer**, for example free text: the skill restates the three
  options and asks again.

If the plan skill stops with an error, the skill prints the error. The
topic file stays `ready for plan`. Run `/sdlc:plan` with the same topic-file
paths to try again.
