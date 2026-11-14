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

Give exactly one input flag, one time. Put a value that holds a space in
double quotes, for example `--topic "idea verification"` or
`--file "docs/my reqs.md"`. The quotes are not part of the value.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--topic <name>` | The name of a preplan topic file in `.sdlc-v2/preplan/` of the main worktree. Give the name, not a path. The topic file must be a regular file: the tool refuses a link. | — |
| `--file <path>` | A requirements file. A relative path starts at the active worktree. The tool follows a link. The tool does not limit the path to the worktree: an absolute path or a path with `..` reads any file that your user can read. | — |
| `--jira <KEY-123>` | A Jira issue key, for example `INT-4571`. | — |
| `--issue <number>` | The number of a GitHub issue in the current repository. | — |

With no flag, two or more input flags, one flag given two times (for example
`--topic a --topic b`), or a flag with no value, the skill prints this message
and stops:

    Give exactly one input: --topic <name>, --file <path>, --jira <KEY-123> or --issue <number>.

For `--file`, the tool checks that the path is a regular file of at most
8 MiB. Then it reads only the first 4096 bytes, to see if line 1 starts with
`# Preplan:`. It returns no file text. The skill reads the file itself with
the `Read` tool.

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

## Statements

The skill splits the input text into requirement statements and scope limits.
A requirement statement states a need. A scope limit says what the change does
not build.

| Input | Requirement statements | Scope limits |
|-------|------------------------|--------------|
| Topic file | The body of `## Goal`, `## Problem and benefit`, `## Users and effect` and `## Risk if wrong`. Each `## Flows` diagram with its text twin. Each live `## Decisions` row: a row whose Decision cell does not start with `Moved:` or `Dropped:`. | Each `## Non-goals` bullet. A bullet `Offered, not picked: <option>.` is an option that the user refused. It is a scope limit, never a need. |
| File, Jira ticket, GitHub issue | Each sentence or bullet that states a need. | Each sentence or bullet that excludes scope. |

These texts of a topic file are not statements and not scope limits: the line
`No flow change.`, the line `None stated.`, the `Smallest cut:` line, the
`**Status:**` line, the `**Guardrail check:**` line or a `## Guardrail check`
section, the `# Preplan:` title, and the `## Open questions` bullets. An empty
or absent section gives no statement. The `empty-section` structure finding
reports it.

These sources follow the rules that `/plan` uses to read a topic file.

## Checks

The skill runs 4 checks. Each check shows in the report as `ran`, `did not
run` or `ran with gaps`.

| Check | Input kinds | Source | Cases of "did not run" |
|-------|-------------|--------|------------------------|
| Structure | topic only | Fixed rules on the topic file (see **Structure rules**). The row comes from the `structureChecks` field of the tool only. | `did not run: the input is not a topic file` for a file. `did not run: the input is a Jira ticket` for a Jira ticket. `did not run: the input is a GitHub issue` for an issue. For a topic file, the check always runs, also when the file has no requirement statement. |
| Guardrails | all | The plan guardrails of the repository. Each guardrail has `id`, `severity` and `description`. A guardrail with no `severity` counts as `error`. The skill compares each requirement statement with each guardrail. It does not compare a scope limit. | `did not run: guardrails unavailable: <warning>` when the guardrails cannot load. `did not run: no guardrails loaded` when the repository has 0 guardrails. `did not run: the input holds no requirement statement`. |
| Meaning | all | The requirement statements and the scope limits. The skill compares each statement with each other statement and with each scope limit. It looks for a contradiction, a statement that builds what a scope limit excludes, or one term with two meanings. | `did not run: fewer than 2 texts to compare`. `did not run: the input holds no requirement statement`. |
| OpenSpec | all | The requirement headings of the main specs and of the active changes in `openspec/`. The skill does not match a scope limit. | `did not run: openspec/config.yaml not found` when the repository has no OpenSpec. `did not run: the input holds no requirement statement`. `ran with gaps: <n> gap(s)` when part of the specs is not listed or not read (see **OpenSpec lists**). |

When the input holds no requirement statement, the skill still sets the
Structure row (`ran: <n> finding(s)` for a topic file, the `structureChecks`
reason for the other inputs). It adds the open question
`Q1  the input holds no requirement statement`. For a topic file, it also
copies each `## Open questions` bullet and each structure finding.

The report also lists open questions. For a topic file, these are the `- `
bullets of `## Open questions`, after the no-statement question when there is
one. For other inputs, these are the gaps that the skill finds in the text: a
need with no user, no effect, no failure case, no measure, or a term with no
meaning. When the input holds no requirement statement, the only open question
of a file, a Jira ticket or an issue is the no-statement question.

## OpenSpec lists

The tool gives the OpenSpec lists of the active worktree. It reads only the
requirement headings. The skill reads a spec only when its headings touch a
statement.

- `openspecStatus` is `present`, or `not present: openspec/config.yaml not
  found`. When `openspec/config.yaml` exists but cannot be checked for another
  reason, the status is `present`, and a warning line names the cause.
- A requirement heading can have up to 3 spaces before it.
- In the spec of an active change, each heading starts with its section:
  `ADDED: `, `MODIFIED: `, `REMOVED: ` or `RENAMED: `. A `REMOVED` or a
  `RENAMED` heading is not a requirement that a statement can conflict with.
  A `RENAMED` section lists `FROM` and `TO` lines.
- Each active change has a `proposal` field: the path of its `proposal.md`.
  An OpenSpec finding of an active change names this path. The skill does not
  read the file.
- Limits: the tool lists at most 1000 spec paths in total. The main specs and
  the specs of the active changes share this limit. The tool does not list a
  path after the first 1000, and it adds one warning line. The tool reads at
  most 1 MiB of each spec. A larger spec is listed with `requirements: []`
  and one warning line.

The `openspecWarnings` lines have these shapes:

| Line | Cause |
|------|-------|
| `<path>: not read: <cause>` | A spec file, a folder (`openspec/specs`, `openspec/changes`, `openspec/changes/<name>/specs`), `openspec/config.yaml` or the `proposal.md` of a change could not be read. |
| `<spec path>: open code fence at line <n>; later requirement headings are not listed` | A code fence in the spec never closes. |
| `<spec path>: no requirement heading found` | The spec holds no requirement heading. |
| `openspec: more than 1000 spec paths; the first 1000 are listed` | The 1000-path limit applies. |

`ran with gaps: <n> gap(s)` counts the gaps. A gap is a distinct path of an
`openspecWarnings` line (the text before the first `: `, so the limit line
counts as the path `openspec`), or the path of a spec that the skill could not
read. A path in both counts one time. The skill prints each warning line and
each failed path under the checks table as `OpenSpec gap: "<line>"`. It prints
these lines also when the OpenSpec row is `did not run`.

## Structure rules

The structure check runs on a topic file only. It has 6 rules. A finding shows
the rule id, the section, the row and one plain sentence.

| Rule id | Trigger | Sample finding |
|---------|---------|----------------|
| `open-fence` | A code fence never closes. Each section after it counts as absent. | `A code fence that starts with ``` never closes. Each section after it counts as absent.` |
| `empty-section` | A section of the topic file is absent, or has a heading and no body. `## Decisions` with no table row counts as empty. | `Section ## Goal is empty.` |
| `decisions-table` | The `## Decisions` table does not parse, a Decisions row has fewer than 3 cells, or the `Smallest cut:` line does not parse. The finding names the row when there is one. | `The ## Decisions table does not parse. The cut and moved checks did not run.` |
| `cut-not-live` | The `Smallest cut` line names a row that does not exist, or a row that starts with `Dropped:` or `Moved:`. | `Smallest cut names #4, but row #4 starts with Dropped:.` |
| `moved-no-question` | More rows start with `Moved:` than `## Open questions` has bullets. | `2 rows start with Moved:, but ## Open questions holds 1 bullet.` |
| `flow-no-proposal` | `## Flows` marks new or changed steps (`:::new` or `:::changed` in a mermaid block), but no live decision row exists. | `## Flows marks new or changed steps, but no live proposal exists.` |

## Report

The skill prints one report and ends. The report has this order:

1. The header line: `Requirements check: --<flag> <value> (<verifySource>)`.
   `verifySource` is the absolute file path for a topic file and a file, the
   issue key for a Jira ticket, and `#<number>` for an issue.
2. The summary line of the tool: `Tool summary: "<summary>"`.
3. The checks table.
4. One `OpenSpec gap:` line for each OpenSpec gap.
5. The 4 finding groups. The report always shows the 4 groups, also a group
   with 0 findings.
6. `No file, ticket or issue changed.`
7. `Next: correct the requirements at their source, or run /sdlc:plan.`

The tool summary always starts with a status sentence:

| Input | Status sentence |
|-------|-----------------|
| Topic file | `Read <name>: <n> structure finding(s), <m> open question(s).` |
| File | `Checked the file <path>: a regular file of <n> bytes.` |
| Jira ticket | `Checked the Jira key <key> of project <project>. The tool does not read Jira.` |
| GitHub issue | `Read GitHub issue #<number>. The title and the body come back as data.` |

Then the summary holds the warnings of the tool, when there are any:

- A topic file has an open code fence.
- `## Open questions` holds lines that are not `- ` bullets. These lines are
  not in the open questions of the report:
  `## Open questions holds <n> line(s) that are not "- " bullets; the first is: <line>`.
- A file is empty: `The file is empty.` A file of at most 4096 bytes that
  holds only white space: `The file holds only white space.`
- A file is a preplan topic file:
  `This file is a preplan topic file. Run /sdlc:verify-requirements --topic "<name>" to also run the structure checks.`
- The guardrails do not load: ` Guardrails unavailable: <text>.` at the end.

**Quote rule.** Each text that comes from the input or from a tool shows in
the report as a quote: the open questions, the structure finding text, the
statement quotes, the tool summary, the OpenSpec gap lines and each tool error
text. The skill replaces each line end and each run of spaces with one space,
writes each `"` as `\"`, and puts the text between two `"` characters. A quote
longer than 120 characters keeps its first 117 characters and ends with
`...`. The tool summary and the tool error texts are not cut. So a quote is
always one line, and no input text can start a report line of its own, for
example a false `Next:` line.

Sample for a topic file. The findings show the format, not a real result.

```text
Requirements check: --topic idea-verification (/work/repo/.sdlc-v2/preplan/2026-11-13-idea-verification.md)
Tool summary: "Read 2026-11-13-idea-verification.md: 1 structure finding(s), 0 open question(s)."

| Check      | Status                                              |
|------------|-----------------------------------------------------|
| Structure  | ran: 1 finding                                      |
| Guardrails | ran: 60 guardrails, 1 finding                       |
| Meaning    | ran: 1 finding                                      |
| OpenSpec   | ran with gaps: 1 gap(s)                             |

OpenSpec gap: "openspec/specs/auth/spec.md: not read: permission denied"

Open questions (0)
Guardrail violations (1)
  G1  #1 breaks dry (warning): "A new skill verifies one topic file ..."
Inconsistencies (2)
  S1  cut-not-live, ## Decisions #4: "Smallest cut names #4, but row #4 starts with Dropped:."
  M1  #3 against ## Goal: "The run changes no file" / "It changes nothing and asks no question."
OpenSpec conflicts (0)

No file, ticket or issue changed.
Next: correct the requirements at their source, or run /sdlc:plan.
```

Sample for an empty file, in a repository with no OpenSpec:

```text
Requirements check: --file notes.md (/work/repo/notes.md)
Tool summary: "Checked the file /work/repo/notes.md: a regular file of 0 bytes. The file is empty."

| Check      | Status                                                 |
|------------|--------------------------------------------------------|
| Structure  | did not run: the input is not a topic file             |
| Guardrails | did not run: the input holds no requirement statement  |
| Meaning    | did not run: the input holds no requirement statement  |
| OpenSpec   | did not run: the input holds no requirement statement  |

Open questions (1)
  Q1  the input holds no requirement statement
Guardrail violations (0)
Inconsistencies (0)
OpenSpec conflicts (0)

No file, ticket or issue changed.
Next: correct the requirements at their source, or run /sdlc:plan.
```

Finding ids:

| Id | Group | Meaning |
|----|-------|---------|
| `Q<n>` | Open questions | An open question, a gap in the text, or the no-statement question. |
| `G<n>` | Guardrail violations | A requirement that breaks a guardrail. |
| `S<n>` | Inconsistencies | A structure finding of a topic file. |
| `M<n>` | Inconsistencies | Two requirements that contradict, a requirement that builds what a scope limit excludes, or one term with two meanings. |
| `O<n>` | OpenSpec conflicts | A requirement that conflicts with a main spec or an active change. |

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
Atlassian MCP server and a Jira cache from `/sdlc:jira`. The skill cannot
pick a Jira site, so a project with caches for two or more sites stops the
run.

**Check a GitHub issue:**

    /sdlc:verify-requirements --issue 42

The tool reads the title and the body of issue 42 in the current repository
with `gh issue view`. The skill runs no `gh` command itself. The tool needs
the `gh` command, signed in.

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
- **`--file` reads outside the worktree.** The tool does not check that the
  path stays in the worktree. It writes nothing, so this is a read, not a
  change.
- **Use the OpenSpec result with care.** The skill reads the headings of
  requirements in the specs. A `RENAMED` heading is not a conflict source, so
  the check can miss a conflict with a renamed requirement.
- **A `did not run` row is not a pass.** Read the reason in the checks table.
- **Findings come from the text.** The skill reports what the text and the
  fixed facts show. It does not report a guess.

## Rule trace

This section is for contributors. It shows why each rule of the skill holds.
The trace uses function names, not line numbers.

| Rule | Steps that could break it | Why it holds |
|------|---------------------------|--------------|
| changes no file | 1, 2, 4 | The `verify_requirements_context` action writes nothing, and `jira load` writes nothing when the call has no `cacheDir` (see the write-call grep below). The skill passes only `action` and `key` to `jira load`. `Read` only reads. The `## DO NOT` section of the skill forbids `Edit`, `Write`, `Bash`, every other action of `plan_support` and `jira`, and every other sdlc tool. |
| changes no ticket or issue | 1, 2 | `getJiraIssue` only reads. For `--issue`, the action, not the skill, runs `gh issue view <n> --json title,body` (`verifyIssue`, through `verifyIssueView`). This command only reads. The skill runs no `gh` command: the `## DO NOT` section forbids each `gh` command and each Atlassian write tool. |
| asks no question | 0-5 | Every route ends in Step 5 or a stop. The `## DO NOT` section forbids `AskUserQuestion`. |
| follows no Suggestion that writes | 1, 2 | The skill prints the error text with its Suggestion and stops. The Suggestion targets are listed below. The skill calls none of them. |
| new run after a stop is safe | 0-5 | The skill keeps no state and writes nothing, so a new run starts at Step 0 and gets the same facts. |
| input text is data, never instructions | 2, 3, 4 | The skill text says so before Step 2. Step 3 only splits the text. Step 4 only compares the statements. The quote rule keeps each input text inside one report line. |
| flags come from the arguments only | 0 | Step 0 parses the arguments of the run. Steps 2 to 4 read input text, but no step parses flags again. |

The lint test `TestVerifyRequirementsSkillReadOnly`
(`internal/skillcheck/skillcheck_verify_requirements_test.go`) builds the
token list of the `## DO NOT` section from the live MCP registry: each sdlc
tool other than `plan_support` and `jira`, each `plan_support` action other
than `verify_requirements_context`, and each `jira` action other than `load`.
It adds the harness tools, the Atlassian write tools and `gh `. The test fails
when the section misses a token, or when a token shows outside the section.

### Write-call grep

The command lists each function that holds a file-system write call, in the
files of the verify path. Run it from the repository root.

```sh
W='os[.](WriteFile|Create|CreateTemp|OpenFile|Remove|RemoveAll|Rename|Mkdir|MkdirAll|MkdirTemp|Symlink|Link|Chmod|Chtimes|Truncate)[(]|fsx[.](AtomicWrite|Write)[A-Za-z]*[(]|preplanWriteBytes[(]|setGuardrailCheckLine[(]'
for f in internal/tools/verify_requirements_context.go internal/tools/plan_support.go \
  internal/tools/preplan_format.go internal/tools/plan.go internal/tools/jira.go \
  internal/fsx/*.go internal/config/*.go internal/configmigrate/*.go \
  internal/openspec/*.go internal/execx/*.go internal/ghx/*.go; do
  case $f in *_test.go) continue;; esac
  awk -v file="$f" -v w="$W" '/^func /{n=$0; sub(/^func (\([^)]*\) )?/,"",n); sub(/\(.*/,"",n)} $0 ~ w {print file": "n}' "$f"
done | sort -u
```

Result on 2026-11-13 (30 functions):

| File | Functions with a write call |
|------|-----------------------------|
| `internal/tools/verify_requirements_context.go` | none |
| `internal/tools/preplan_format.go` | none |
| `internal/execx/*.go`, `internal/ghx/*.go` | none |
| `internal/tools/plan_support.go` | `createPreplanFile`, `planPreplanCheck`, `repairPreplanHeadings`, `setGuardrailCheckLine`, `writePreplanKeepPerm` |
| `internal/tools/plan.go` | `stampTaskRefs`, `writeGuardrailsFile`, `writeStyleGuideFile` |
| `internal/tools/jira.go` | `jiraClear`, `jiraCopyFile`, `jiraCopyTemplate`, `jiraGetCachePath`, `jiraInitTemplates`, `jiraSave`, `jiraSaveField`, `jiraWriteApproval`, `jiraWriteCritique` |
| `internal/config/config.go` | `DeleteSectionReport`, `WriteFileSection`, `writeSectionFile`, `WriteSectionReport` |
| `internal/configmigrate/movedkeys.go` | `MigrateMovedKeys` |
| `internal/fsx/dangling.go`, `internal/fsx/fsx.go` | `MkdirAll`, `atomicWriteBytes` |
| `internal/openspec/materialize.go`, `internal/openspec/stage.go` | `Materialize`, `acquireStagingLock`, `newTempChange`, `writeFile`, `writeStagingLocked`, `writeStagingTemp` |

The verify path calls none of these functions:

- `planSupportCore` sends the action to `planVerifyRequirementsContext`. That
  function calls `verifyTopic` (`preplanReadTopicFile`, which uses
  `preplanMatches`, `preplanNoTopicFile`, `readPreplanTopic`,
  `preplanIOError` and `preplanNotRegularError`; then
  `preplanStructureFindingsOf` and `verifyOpenQuestions`), `verifyFile`,
  `verifyJira`, `verifyIssue` (`verifyIssueView`, which calls
  `ghx.IssueViewJSON` to run `gh issue view`), `loadGuardrails`
  (`config.ReadSection`) and `verifyOpenspec` (`os.Stat`, `os.ReadDir`,
  `os.ReadFile`). The preplan write functions belong to the `preplan_context`,
  `preplan_check` and `preplan_write` actions.
- `jiraCore` runs `configmigrate.Verify`, then `jiraLoad`. `jiraLoad` calls
  `jiraResolveEffectiveCachePath` and `fsx.ReadJSON`.
  `jiraResolveEffectiveCachePath` calls `jiraGetCachePath`, which makes the
  cache folder and can write a `.gitignore`, only when the call has a
  `cacheDir`. The skill passes only `action` and `key`, so `jira load` writes
  nothing.

### Suggestion grep

The command lists the Suggestion texts that the verify path can return.

```sh
grep -nE 'Suggestion' internal/tools/verify_requirements_context.go
for fn in preplanMatches preplanNoTopicFile readPreplanTopic preplanIOError preplanNotRegularError jiraLoad; do
  awk -v fn="$fn" '$0 ~ "^func "fn"[(]" {p=1} p && /Suggestion/ {print fn": "$0} p && /^}/ {p=0}' \
    internal/tools/plan_support.go internal/tools/jira.go
done
```

Suggestion targets found on 2026-11-13 for the `verify_requirements_context`
action, with the constants and helpers that the Suggestions use
(`verifyRunAgain`, `verifyNoFileSuggestion`, `preplanRunSkillAgain`,
`preplanNoFileSuggestionFor` and `fsx.DanglingLinkError.Recovery`). The
preplan helpers pick the text from the action, so the `/sdlc:preplan` rerun
texts and `preplanGoneSuggestion` of the other actions do not show here:

| Target | Kind | Source |
|--------|------|--------|
| `/sdlc:verify-requirements` (run again, or `--file <path>`) | skill | `verifyRunAgain`, `verifyNoFileSuggestion`, `preplanRunSkillAgain` (in `preplanMatches`), `verifyIssueViewError` (output limit) |
| `/sdlc:preplan` (make a new topic file) | skill | `preplanNotRegularError` |
| `jira check`, `jira save` | `jira` actions | `jiraLoad` |
| `gh auth login`, `gh issue view <n>`, install `gh` | shell commands | `verifyIssueViewError`, `verifyIssue` |
| `rm <file>`, `mkdir -p <folder>` | shell commands | `preplanNotRegularError`, `DanglingLinkError.Recovery` |
| edit, rename, delete or copy a file by hand | manual step | `preplanMatches`, `readPreplanTopic`, `preplanNotRegularError`, `jiraLoad`, `verifyFile` |

The skill calls none of these targets. It prints the Suggestion as text and
stops. The `## DO NOT` section forbids `Skill` (so the skill starts no other
skill), `Bash` and `gh ` (so it runs no shell command), `preplan_context`,
`jira check` and `jira save`, and `Edit` and `Write`.

Run both greps again after a change to `verify_requirements_context.go`, to
the preplan helpers or to `jiraLoad`, and update these tables.
