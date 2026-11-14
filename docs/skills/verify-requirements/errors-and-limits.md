# Errors and limits

Read this page when a run stops with a message and you want the next action,
or when you want to know what the skill never does. The short overview is
[/sdlc:verify-requirements](../verify-requirements.md).

## Example

```text
/sdlc:verify-requirements --issue 42 --topic x
```

The command gives 2 input flags. The skill makes no tool call, prints this
message and stops:

```text
Give exactly one input: --topic <name>, --file <path>, --jira <KEY-123> or --issue <number>.
```

## Errors

Each error stops the run. The skill prints the message and the next action, and
it makes no other call. It prints each tool error text as a quote. The tool
messages below show the text after the short label that the tool puts at the
start of each message.

| Cause | Message | Next action |
|-------|---------|-------------|
| 0, 2 or more input flags, one flag given two times, or a flag with no value | `Give exactly one input: --topic <name>, --file <path>, --jira <KEY-123> or --issue <number>.` | Run the skill again with one input flag. |
| No topic file for the name, or the topic file went away after the folder listing | `no topic file for topic "<name>" in <folder>`, or the os error of the file | Check the topic name against the files in `.sdlc-v2/preplan/`. For a file outside that folder, run `/sdlc:verify-requirements --file <path>`. |
| The name matches 2 or more topic files | `topic "<name>" matches <n> topic files in <folder>: <list>` | Rename or delete one of the files in the list. Then run `/sdlc:verify-requirements` again. |
| The preplan folder cannot be listed | `list: <os error>` (the os error names the folder) | Fix the cause. Then run `/sdlc:verify-requirements` again. |
| The preplan folder holds more than 10000 names | `list <folder>: the folder holds more than 10000 names` | Move old topic files out of the folder until it holds at most 10000 names. Then run `/sdlc:verify-requirements` again. |
| A dangling link at the preplan folder or at a folder above it | `list <folder>: <problem>` | Repair or remove the link, as the message says. Then run the skill again. |
| A path given to `--topic` | The name finds no topic file, and the message above shows. | Use `--file <path>` for a path. |
| The topic file is a link, a folder or another file that is not regular | `<file> is <kind>, not a regular file`, or a message for a link to a missing or unreadable target | For a link to a file that exists: replace the link with a copy of that file. Otherwise remove the file or the link, then run `/sdlc:preplan` to make a new topic file, and run `/sdlc:verify-requirements` again. |
| The topic file is over 8 MiB | `<file> is <n> bytes, more than the 8388608 bytes (8 MiB) that verify_requirements_context reads` | Make the topic file smaller, then run the skill again. |
| The kind is not one of the four | `verifyKind must be one of topic, file, jira, issue` | Use one of the four input flags. The skill sets the kind from the flag, so this error shows only when the tool is called by hand. |
| The value is blank | `verifyValue is required` | Give a value: the topic name, the file path, the Jira key or the issue number. |
| The value is longer than 4096 bytes | `verifyValue is <n> bytes; the limit is 4096 bytes` | Give a shorter value. |
| No file at the path | `no file at <path>` | Check the path. A relative path starts at the active worktree. |
| The path is a folder | `<path> is a folder, not a file` | Give the path of one requirements file. |
| The path is a device, a pipe or a socket, not a regular file | `<path> is not a regular file` | Give the path of one requirements file. |
| The file is over 8 MiB | `<path> is <n> bytes; the limit is <limit> bytes (8 MiB)` | Split the requirements into smaller files and check each one. |
| The file cannot be read | `cannot read <path>: <cause>` | Check the file permissions, then run the skill again. |
| The Jira value is not a key | `<value> is not a Jira key` | Give a key such as `INT-4571`: the project key, a hyphen and the issue number. |
| The issue value is not a number | `verifyValue must be a GitHub issue number of 1 to <n> digits with no leading zero` | Give only the digits, for example `42`. |
| The `gh` command is not installed | `the gh CLI is not installed` | Install `gh`, run `gh auth login`, then run the skill again. |
| `gh` is not signed in (exit code 4, or `gh auth login` in its error output) | `gh is not authenticated` | Run `gh auth login`, then run the skill again. |
| The issue does not exist (`Could not resolve to an issue` in the error output of `gh`) | `issue #<number> not found in this repository` | Check the issue number in the GitHub repository of the active worktree. |
| `gh` prints more than the output limit | `gh issue view <number> printed more than the output limit` | Copy the requirements into a file, then run the skill with `--file <path>`. |
| `gh` fails for another reason | `gh issue view <number> failed: <first error line, at most 200 bytes>` | Run `gh issue view <number>` to see the cause, then run the skill again. |
| `gh` returns text that is not valid JSON | `gh issue view <number> returned invalid JSON` | Update `gh`, then run the skill again. |
| The issue has no title and no body | `issue #<number> has no title and no body` | Add the requirements to the issue, or give another input. |
| The skill cannot read the input file | `Cannot read <path>: "<error text>". Check the file, then run /sdlc:verify-requirements again.` | Check the file, then run the skill again. |
| The project config is not current, so the `jira` tool runs no action | `The jira tool did not load the cache: "<errors lines>". Run /sdlc:setup to update the project config, then run this skill again.` | Run `/sdlc:setup`, then run the skill again. |
| No Jira cache for the project, or a cache with no site id | `No Jira cache for <project>. Run /sdlc:jira --project <project> once, then run this skill again.` | Run `/sdlc:jira --project <project>`, then run the skill again. |
| More than one Jira site has a cache for the project | `More than one Jira site has a cache for <project>. This skill cannot pick a site. Copy the ticket text into a file, then run /sdlc:verify-requirements --file <path>.` | The skill has no `--site` input. Copy the ticket text into a file and use `--file`. |
| The Jira cache cannot load for another reason | `jira load failed: "<error text>"`, and its Suggestion as text. | Read the Suggestion as text. The skill does not follow it. |
| The Atlassian MCP server is absent, or the ticket cannot be read | `Cannot read <key>: "<tool error text>". Connect the Atlassian MCP server or check the key, then run /sdlc:verify-requirements --jira <key> again.` | Connect the server or check the key, then run the skill again. |

A tool Suggestion can name a skill, a tool or a shell command that changes a
file. The skill prints that Suggestion as text and never runs what it names.

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

## Tips and gotchas

- **Correct the source, not the report.** The skill changes nothing. Fix the
  topic file, the file, the ticket or the issue yourself. Then run the skill
  again.
- **A topic name is not a path.** `--topic` takes a name. For a file outside
  `.sdlc-v2/preplan/`, use `--file`.
- **`--file` reads outside the worktree.** The tool does not check that the
  path stays in the worktree. It writes nothing, so this is a read, not a
  change.
- **Use the OpenSpec result with care.** The skill reads the headings of
  requirements in the specs. A `RENAMED` heading is not a conflict source, so
  the check can miss a conflict with a renamed requirement.
- **A `did not run` row is not a pass.** Read the reason in the checks table.
- **Findings come from the text.** The skill reports what the text and the
  fixed facts show. It does not report a guess.
