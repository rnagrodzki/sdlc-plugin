# Inputs and checks

Read this page when you want to know which text the skill checks, what each
of the 4 checks compares, or why a check shows `did not run`. The short
overview is [/sdlc:verify-requirements](../verify-requirements.md).

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

A message for each bad input is in [Errors](./errors-and-limits.md#errors).

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

The status text of each check row is in
[Checks table](./report.md#checks-table).

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
