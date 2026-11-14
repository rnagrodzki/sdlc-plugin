# /sdlc:preplan

Answer one question at a time, and turn a vague idea into a topic file that `/sdlc:plan` reads as its requirements.

## Why use it

You have an idea, but it is still vague. The goal, the users, the risk and the scope are not written down.
`/sdlc:preplan` asks you one question at a time and writes each answer into one topic file. It checks each proposal against your plan guardrails, so you find a conflict before a plan run finds it.
The skill starts no plan run and writes no plan. It works while plan mode is active.

## Example

```text
/sdlc:preplan auth flow
```

What happens:

1. On 2026-10-12 (UTC), the skill creates `.sdlc-v2/preplan/2026-10-12-auth-flow.md` and asks the first question, about the goal.
2. Each answer goes into the file before the next question.
3. Each proposal is checked against your plan guardrails before you see it as accepted.
4. When you answer "done" and the final check passes, the file gets the status `ready for plan`.
5. The skill asks what to do next: start `/sdlc:plan` with this file, start a new idea, or stop.

## What you get

- **Think the idea through before you plan.** The questions are about function and effect: who uses the change, what changes, and what fails. None is about code.
- **Find a guardrail conflict early.** The skill reworks or drops a proposal that breaks an `error` guardrail, and asks you about a `warning` guardrail.
- **Get a written record.** One Markdown file holds the goal, the problem, the users, the risk, the non-goals, the flows and the decisions.
- **Stop and continue later.** The topic file is the only state. Run the command again with the same topic, and the dialogue goes on from the file.
- **Go from the idea to the plan in one session.** At the end, start `/sdlc:plan` with one or more ready topic files.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Announce and take the topic | skill | Prints the announce line and takes the topic from the argument. It asks for the topic when the argument is empty. | The topic name | no | always |
| Step 1: Load the guardrails and the topic file | tool | Calls `preplan_context` to load the plan guardrails and to create the topic file, or to add the absent section headings. | The guardrails and the topic file path | no | always |
| Step 2: Read the topic file | skill | Reads the topic file, sets the status to `in progress` and converts an old-form file. | The gaps in the topic file | no | always |
| Step 3: Ask one question | skill | Asks one question about the first gap and writes the answer into its section. | An updated topic file section | no | until no section is empty or the user is done |
| Step 4: Check the proposals against the guardrails | skill | Checks each live proposal against the guardrails. It reworks or drops a proposal that breaks an `error` guardrail. | A topic file with a `**Guardrail check:**` line | no | after each Step 3 answer |
| Step 5: Hand off to plan | skill | Sets the status to `ready for plan` and asks what to do next: start `/sdlc:plan`, start a new idea, or stop. | A topic file with the status `ready for plan` | no | the check passes with no empty section |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:preplan [topic]`. The skill creates or reopens the topic file of the topic. |
| Ends | After Step 5, when the user starts plan, starts a new idea or stops. The user can cancel at any step, and the status becomes `paused`. |
| Asks the user | One question at a time, for the topic when it is absent and then for each gap in the topic file. |
| Resume | Run `/sdlc:preplan <topic>` again. The topic file is the only state, so the skill continues from it. |

## More examples

### Start without a topic

```text
/sdlc:preplan
```

The skill asks for a short topic name first. If you cancel at this point, no file is created.

### Continue a topic later

```text
/sdlc:preplan auth flow
```

The file `2026-10-12-auth-flow.md` already exists, so a run on a later day continues it. The status becomes `in progress`, and the skill lists the open questions, if any. Then the dialogue goes on from the first gap.

### Plan from a topic file without the handoff

```text
/sdlc:plan .sdlc-v2/preplan/2026-10-12-auth-flow.md
```

Use this after you chose "Stop" at the handoff, or when the plan skill stopped with an error.

## Flags

| Flag | Meaning |
|---|---|
| `[topic]` | Short topic name. It becomes part of the topic file name. With no topic, the skill asks you for one. See [Topic name](./preplan/topic-file.md#topic-name). |

## Reference

- [Topic file](./preplan/topic-file.md) — the file name, the sections, the status values, the format rules and the errors at the start.
- [Dialogue and handoff](./preplan/dialogue.md) — the order of the questions, the cut question and the options at the handoff.
- [Guardrail check](./preplan/guardrail-check.md) — what the skill does when a proposal breaks a guardrail, and the errors that stop the check.

## Related skills

- [/sdlc:plan](plan.md) — Plans the work after the topic file is ready. Use it directly when your requirements are already clear.
- [/sdlc:verify-requirements](verify-requirements.md) — Checks a topic file with no dialogue and prints a report of findings.
