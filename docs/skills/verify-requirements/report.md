# Report

Read this page when you have a report of `/sdlc:verify-requirements` and you
want to know what a line means, or when a program reads the report. The
short overview is [/sdlc:verify-requirements](../verify-requirements.md).

## Sample report

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

## Order of the report

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

## Checks table

Each check has one row. The row shows `ran`, `did not run` or
`ran with gaps`.

| Check | `ran` | `did not run` | `ran with gaps` |
|-------|-------|---------------|-----------------|
| Structure | `ran: <n> finding(s)` | `did not run: the input is not a topic file`, `did not run: the input is a Jira ticket` or `did not run: the input is a GitHub issue` | — |
| Guardrails | `ran: <n> guardrails, <m> finding(s)` | `did not run: guardrails unavailable: <warning>`, `did not run: no guardrails loaded`, or the 0-statement text | — |
| Meaning | `ran: <n> finding(s)` | `did not run: fewer than 2 texts to compare`, or the 0-statement text | — |
| OpenSpec | `ran: <n> main specs, <m> active changes, <k> finding(s)` | `did not run: openspec/config.yaml not found`, or the 0-statement text | `ran with gaps: <n> gap(s)` |

The 0-statement text is
`did not run: the input holds no requirement statement`. It never applies to
the Structure row. The cause of each status is in
[Checks](./checks.md#checks).

## Tool summary

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

## Quote rule

Each text that comes from the input or from a tool shows in
the report as a quote: the open questions, the structure finding text, the
statement quotes, the tool summary, the OpenSpec gap lines and each tool error
text. The skill replaces each line end and each run of spaces with one space,
writes each `"` as `\"`, and puts the text between two `"` characters. A quote
longer than 120 characters keeps its first 117 characters and ends with
`...`. The tool summary and the tool error texts are not cut. So a quote is
always one line, and no input text can start a report line of its own, for
example a false `Next:` line.

## Finding ids

| Id | Group | Meaning |
|----|-------|---------|
| `Q<n>` | Open questions | An open question, a gap in the text, or the no-statement question. |
| `G<n>` | Guardrail violations | A requirement that breaks a guardrail. |
| `S<n>` | Inconsistencies | A structure finding of a topic file. |
| `M<n>` | Inconsistencies | Two requirements that contradict, a requirement that builds what a scope limit excludes, or one term with two meanings. |
| `O<n>` | OpenSpec conflicts | A requirement that conflicts with a main spec or an active change. |

Each id letter counts from 1, in the order of the findings. Each finding is
one line. Inconsistencies list the `S` findings first, then the `M`
findings.

## Sample report for an empty file

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
