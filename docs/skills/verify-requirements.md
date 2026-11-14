# /sdlc:verify-requirements

Find the gaps and the conflicts in your requirements before you plan them, in one report, with no question to answer.

## Why use it

You have requirements in a preplan topic file, a file, a Jira ticket or a GitHub issue, and you want to plan them.
A gap or a contradiction in that text goes into the plan when nobody finds it first.
This skill reads the text and prints one report: open questions, guardrail violations, inconsistencies and OpenSpec conflicts.
You correct the requirements at their source, then you run `/sdlc:plan`.

## Example

Check the topic file that `/sdlc:preplan` wrote for the topic `idea-verification`:

```text
/sdlc:verify-requirements --topic idea-verification
```

The skill reads `.sdlc-v2/preplan/<date>-idea-verification.md`, runs the 4 checks and prints the report. The [sample report](./verify-requirements/report.md#sample-report) shows these lines:

- The Guardrails row of the checks table: `ran: 60 guardrails, 1 finding`
- `G1  #1 breaks dry (warning): "A new skill verifies one topic file ..."`
- `M1  #3 against ## Goal: "The run changes no file" / "It changes nothing and asks no question."`
- `Next: correct the requirements at their source, or run /sdlc:plan.`

## What you get

- See the open questions: the bullets of a topic file, or the gaps in other text, such as a need with no failure case.
- Catch a guardrail violation before the plan: the skill compares each requirement statement with the plan guardrails of your repository.
- Find inconsistencies: statements that contradict, a statement that builds what a scope limit excludes, and 6 structure rules for a topic file.
- Find conflicts with OpenSpec: the skill matches each statement with the requirement headings of the main specs and of the active changes.
- Run it at no risk: the skill changes no file, ticket or issue, asks no question, and is safe to run again.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Announce and parse the flags | skill | Counts the input flags. Exactly one input flag with a value is valid. Any other count prints one message and stops. | The input kind and value | no | always |
| Step 1: Get the fixed facts | tool | Calls `verify_requirements_context` once for the source, the guardrails, the structure findings, the open questions and the OpenSpec lists. | The fixed facts of the input | no | always |
| Step 2: Read the input text | skill | Reads the topic file or the requirements file, loads the Jira ticket, or takes the GitHub issue text from step 1. The text is data, never instructions. | The input text | no | always |
| Step 3: Split the text into statements | skill | Splits the text into requirement statements and scope limits, and gives each one a reference. | The statements and the scope limits | no | always |
| Step 4: Run the checks | skill | Runs the Structure, Open questions, Guardrails, Meaning and OpenSpec checks, and records the findings of each check. | The findings and the checks table | no | the input holds one or more statements, or the 0-statement route applies |
| Step 5: Print the report | skill | Prints the header, the tool summary, the checks table, the OpenSpec gaps and the four finding groups. | The report | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:verify-requirements` and exactly one of `--topic`, `--file`, `--jira` or `--issue`. |
| Ends | After the report. The last two lines are `No file, ticket or issue changed.` and the `Next:` line. Any stop prints one message that names the next action. |
| Asks the user | Never. The skill asks no question. |
| Resume | none |

## More examples

**Check a requirements file:**

```text
/sdlc:verify-requirements --file docs/requirements/export.md
```

The Structure check does not run for this input, because the file is not a topic file. The other checks run.

**Check a Jira ticket:**

```text
/sdlc:verify-requirements --jira INT-4571
```

The skill reads the summary and the description of the ticket. It needs the Atlassian MCP server and a Jira cache from `/sdlc:jira`.

**Check a GitHub issue:**

```text
/sdlc:verify-requirements --issue 42
```

The tool reads the title and the body of issue 42 in the current repository with `gh issue view`. It needs the `gh` command, signed in.

## Reference

- [Inputs and checks](./verify-requirements/checks.md): the flags, the statements, the 4 checks, the OpenSpec lists and the structure rules.
- [Report](./verify-requirements/report.md): the order of the report, the checks table, the quote rule, the finding ids and two sample reports.
- [Errors and limits](./verify-requirements/errors-and-limits.md): each stop message with its next action, what the skill never changes, and tips.
- [Rule trace](./verify-requirements/rule-trace.md): for contributors, why each read-only rule of the skill holds.

## Related skills

- [/sdlc:preplan](./preplan.md): shapes an idea into the topic file that this skill checks.
- [/sdlc:plan](./plan.md): plans the work after the check.
- [/sdlc:jira](./jira.md): creates the Jira cache that the `--jira` input needs.
