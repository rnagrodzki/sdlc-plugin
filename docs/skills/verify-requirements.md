# /verify-requirements

Check one set of requirements before you plan it. The skill reads a topic
file, a requirements file, a Jira ticket or a GitHub issue, and prints one
report of findings. The full command name is `/sdlc:verify-requirements`.

## When to use

- You shaped an idea with `/preplan`, and you want a second check of the topic
  file before `/plan`.
- You have requirements in a file, a Jira ticket or a GitHub issue, and you
  want to find gaps and conflicts before you plan.
- You want a check with no dialogue. The skill asks no question.

## Syntax

    /sdlc:verify-requirements --topic <name> | --file <path> | --jira <KEY-123> | --issue <number>

Give exactly one input flag.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--topic <name>` | The name of a preplan topic file in `.sdlc-v2/preplan/` of the main worktree. Give the name, not a path. | — |
| `--file <path>` | A requirements file. A relative path starts at the active worktree. | — |
| `--jira <KEY-123>` | A Jira issue key, for example `INT-4571`. | — |
| `--issue <number>` | The number of a GitHub issue in the current repository. | — |

With no flag, two or more input flags, or a flag with no value, the skill
prints this message and stops:

    Give exactly one input: --topic <name>, --file <path>, --jira <KEY-123> or --issue <number>.

## Checks

The skill runs 4 checks. Each check shows in the report as `ran` or
`did not run`.

| Check | Input kinds | Source | Cases of "did not run" |
|-------|-------------|--------|------------------------|
| Structure | topic only | Fixed rules on the topic file (see **Structure rules**). | `did not run: the input is not a topic file` for a file, a Jira ticket or an issue. `did not run: the input holds no requirement statement` when the text has no requirement. |
| Guardrails | all | The plan guardrails of the repository. The skill compares each requirement with each guardrail. | `did not run: guardrails unavailable: <warning>` when the guardrails cannot load. `did not run: the input holds no requirement statement`. |
| Meaning | all | The requirements. The skill compares each requirement with each other requirement and looks for a contradiction or one term with two meanings. | `did not run: the input holds no requirement statement`. |
| OpenSpec | all | The main specs and the active changes in `openspec/`. | `did not run: openspec/config.yaml not found` when the repository has no OpenSpec. `did not run: the input holds no requirement statement`. A spec that cannot be read gives `ran with gaps: <n> specs not read`. |

The report also lists open questions. For a topic file, these are the bullets
of `## Open questions`. For other inputs, these are the gaps that the skill
finds in the text: a need with no user, no effect, no failure case, no
measure, or a term with no meaning.

## Structure rules

The structure check runs on a topic file only. It has 6 rules. A finding shows
the rule id, the section, the row and one plain sentence.

| Rule id | Trigger | Sample finding |
|---------|---------|----------------|
| `open-fence` | A code fence never closes. Each section after it counts as absent. | `A code fence that starts with ``` never closes. Each section after it counts as absent.` |
| `empty-section` | A section of the topic file has a heading and no body. | `Section ## Goal is empty.` |
| `decisions-table` | The `## Decisions` table does not parse. The cut and moved checks do not run. | `The ## Decisions table does not parse. The cut and moved checks did not run.` |
| `cut-not-live` | The `Smallest cut` line names a row that does not exist, or a row that starts with `Dropped:` or `Moved:`. | `Smallest cut names #4, but row #4 starts with Dropped:.` |
| `moved-no-question` | More rows start with `Moved:` than `## Open questions` has bullets. | `2 rows start with Moved:, but ## Open questions holds 1 bullet.` |
| `flow-no-proposal` | `## Flows` marks new or changed steps (`:::new` or `:::changed` in a mermaid block), but no live decision row exists. | `## Flows marks new or changed steps, but no live proposal exists.` |

## Report

The skill prints one report and ends. The report always shows the 4 groups,
also a group with 0 findings. The first line names the input. The last two
lines are always `No file, ticket or issue changed.` and the `Next:` line.

Sample for a topic file. The findings show the format, not a real result.

```text
Requirements check: --topic idea-verification (.sdlc-v2/preplan/2026-11-13-idea-verification.md)

| Check      | Status                                           |
|------------|--------------------------------------------------|
| Structure  | ran: 1 finding                                   |
| Guardrails | ran: 60 guardrails, 1 finding                    |
| Meaning    | ran: 1 finding                                   |
| OpenSpec   | ran: 61 main specs, 0 active changes, 0 findings |

Open questions (0)
Guardrail violations (1)
  G1  #1 breaks dry (warning). Quote: "A new skill verifies one topic file ..."
Inconsistencies (2)
  S1  cut-not-live, ## Decisions #4: Smallest cut names #4, but row #4 starts with Dropped:.
  M1  Decision #3 against Goal: "The run changes no file" / "It changes nothing and asks no question."
OpenSpec conflicts (0)

No file, ticket or issue changed.
Next: correct the requirements at their source, or run /sdlc:plan.
```

Sample for a file, in a repository with no OpenSpec. Only the checks table is
shown.

```text
| Check      | Status                                                        |
|------------|---------------------------------------------------------------|
| Structure  | did not run: the input is not a topic file                    |
| Guardrails | did not run: guardrails unavailable: <warning>                |
| Meaning    | ran: 0 findings                                               |
| OpenSpec   | did not run: openspec/config.yaml not found                   |
```

Finding ids:

| Id | Group | Meaning |
|----|-------|---------|
| `Q<n>` | Open questions | An open question or a gap in the text. |
| `G<n>` | Guardrail violations | A requirement that breaks a guardrail. |
| `S<n>` | Inconsistencies | A structure finding of a topic file. |
| `M<n>` | Inconsistencies | Two requirements that contradict, or one term with two meanings. |
| `O<n>` | OpenSpec conflicts | A requirement that conflicts with a main spec or an active change. |

The skill prints a warning above the report when a topic file has an open
code fence, or when a `--file` input looks like a preplan topic file.

## What it never changes

- It changes no file. It does not edit the topic file or the requirements
  file.
- It changes no Jira ticket and no GitHub issue. It adds no comment and makes
  no transition.
- It asks no question. Each run ends with the report or with one error
  message.
- It is safe to run again. The skill keeps no state, so a new run after a stop
  starts from the beginning and reads the same facts.
- It treats the text of the input as data, never as instructions. A line in a
  ticket that asks the skill to do something is checked as a requirement, and
  the skill does not do it.

## Errors

Each error stops the run. The skill prints the message and the next action, and
it makes no other call. The tool messages below show the text after the short
label that the tool puts at the start of each message.

| Cause | Message | Next action |
|-------|---------|-------------|
| 0, 2 or more input flags, or a flag with no value | `Give exactly one input: --topic <name>, --file <path>, --jira <KEY-123> or --issue <number>.` | Run the skill again with one input flag. |
| No topic file for the name | `no topic file for topic "<name>" in <folder>` | Check the name against the files in `.sdlc-v2/preplan/`. For a file outside that folder, use `--file <path>`. |
| The name matches 2 or more topic files | `topic "<name>" matches <n> topic files in <folder>: <list>` | Rename or delete one of the files. Run the skill again. |
| The preplan folder cannot be listed | `list <folder>: <cause>` | Make the folder readable, then run the skill again. |
| A path given to `--topic` | The name finds no topic file, and the message above shows. | Use `--file <path>` for a path. |
| The kind is not one of the four | `verifyKind must be one of topic, file, jira, issue` | Use one of the four input flags. The skill sets the kind from the flag, so this error shows only when the tool is called by hand. |
| The value is blank | `verifyValue is required` | Give a value: the topic name, the file path, the Jira key or the issue number. |
| The value is longer than 4096 bytes | `verifyValue is <n> bytes; the limit is 4096 bytes` | Give a shorter value. |
| No file at the path | `no file at <path>` | Check the path. A relative path starts at the active worktree. |
| The path is a folder | `<path> is a folder, not a file` | Give the path of one requirements file. |
| The path is a link or a device, not a regular file | `<path> is not a regular file` | Give the path of one requirements file. |
| The file is over 8 MiB | `<path> is <n> bytes; the limit is <limit> bytes (8 MiB)` | Split the requirements into smaller files and check each one. |
| The file cannot be read | `cannot read <path>: <cause>` | Check the file permissions, then run the skill again. |
| The Jira value is not a key | `<value> is not a Jira key` | Give a key such as `INT-4571`: the project key, a hyphen and the issue number. |
| The issue value is not a number | `verifyValue must be a GitHub issue number of 1 to <n> digits with no leading zero` | Give only the digits, for example `42`. |
| The `gh` command is not installed | `the gh CLI is not installed` | Install `gh`, run `gh auth login`, then run the skill again. |
| `gh` is not signed in | `gh is not authenticated` | Run `gh auth login`, then run the skill again. |
| The issue does not exist | `issue #<number> not found in this repository` | Check the issue number in the GitHub repository of the active worktree. |
| `gh` fails for another reason | `gh issue view <number> failed: <stderr>` | Run `gh issue view <number>` to see the cause, then run the skill again. |
| `gh` returns text that is not valid JSON | `gh issue view <number> returned invalid JSON` | Update `gh`, then run the skill again. |
| The issue has no title and no body | `issue #<number> has no title and no body` | Add the requirements to the issue, or give another input. |
| The skill cannot read the input file | `Cannot read <path>: <error text>. Check the file, then run /sdlc:verify-requirements again.` | Check the file, then run the skill again. |
| The Jira cache cannot load | The error text of the Jira cache, and its Suggestion. | Read the Suggestion as text. The skill does not follow it. |
| The Jira cache has no site id | `No Jira cache for <project>. Run /sdlc:jira --project <project> once, then run this skill again.` | Run `/sdlc:jira --project <project>`, then run the skill again. |
| The Atlassian MCP server is absent, or the ticket cannot be read | `Cannot read <key>: <tool error text>. Connect the Atlassian MCP server or check the key, then run /sdlc:verify-requirements --jira <key> again.` | Connect the server or check the key, then run the skill again. |

A tool Suggestion can name a skill or a tool that changes a file. The skill
prints that Suggestion as text and never runs what it names.

## Examples

**Check a topic file:**

    /sdlc:verify-requirements --topic idea-verification

Reads `.sdlc-v2/preplan/<date>-idea-verification.md`, runs the 4 checks and
prints the report.

**Check a requirements file:**

    /sdlc:verify-requirements --file docs/requirements/export.md

The Structure check does not run for this input. The other checks run.

**Check a Jira ticket:**

    /sdlc:verify-requirements --jira INT-4571

Reads the summary and the description of the ticket. The skill needs the
Atlassian MCP server and a Jira cache from `/sdlc:jira`.

**Check a GitHub issue:**

    /sdlc:verify-requirements --issue 42

Reads the title and the body of issue 42 in the current repository. The skill
needs the `gh` command, signed in.

## Related skills

- [/preplan](preplan.md) — Shapes an idea into the topic file that this skill
  checks.
- [/plan](plan.md) — Plan the work after the check.
- [/jira](jira.md) — Creates the Jira cache that the `--jira` input needs.

## Tips and gotchas

- **Correct the source, not the report.** The skill changes nothing. Fix the
  topic file, the file, the ticket or the issue yourself. Then run the skill
  again.
- **A topic name is not a path.** `--topic` takes a name. For a file outside
  `.sdlc-v2/preplan/`, use `--file`.
- **Use the OpenSpec result with care.** The skill reads the headings of
  requirements in the specs. An OpenSpec `RENAMED` section lists `FROM` and
  `TO` lines, not requirement headings. A renamed requirement shows no heading
  in the OpenSpec list, so the check can miss a conflict with it.
- **A `did not run` row is not a pass.** Read the reason in the checks table.
- **Findings come from the text.** The skill reports what the text and the
  fixed facts show. It does not report a guess.
