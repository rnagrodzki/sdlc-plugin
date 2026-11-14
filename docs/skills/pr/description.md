# The PR description

Read this page when you want to know which sections the PR description has,
how the skill fills them, or which checks the draft passes before you see it.
The short overview is [/sdlc:pr](../pr.md).

## Example

```text
/sdlc:pr
```

With no project template, the description has the 8 sections of the table
below, in that order. Before the publish question, the skill shows the draft
in this frame:

```text
PR Title: <title>

PR Description:
─────────────────────────────────────────────
<full description>
─────────────────────────────────────────────
```

## Sections

| Section | Content |
|---|---|
| `## Summary` | 1 to 3 sentences in plain language, with no jargon. |
| `## JIRA Ticket` | The ticket that the branch name carries, for example `PROJ-123`. The section is absent when the branch name carries no ticket. |
| `## Business Context` | Why the business or the product needs the change. |
| `## Business Benefits` | The value of the change: user impact, revenue, efficiency, risk reduction, compliance. |
| `## Technical Design` | The approach, the key decisions, the patterns and the trade-offs. |
| `## Technical Impact` | The systems, services and APIs that the change affects. Breaking changes, migration needs and performance. |
| `## Changes Overview` | Bullets grouped by concern. Each bullet describes a concept or a behavior change. No file paths. |
| `## Testing` | How the change was verified. When no tests were added, the reason. |

The title has fewer than 72 characters. It uses the conventional commit style
(`feat:`, `fix:`, `refactor:`) unless you tell the skill to use a different
style. The skill does not check the title against a project pattern.

## Your own template

When your project has a PR template, the skill uses the headings of the
template as the sections, in the same order. The text below each heading is
the fill instruction for that section. Each heading of the template is in the
description.

- `/sdlc:setup --pr-template` writes the template to
  `.sdlc-v2/pr-template.md`. See
  [Flags and sections](../setup/flags-and-sections.md).
- A template at the old location `.claude/pr-template.md` still works. The
  skill tells you one time to move it to `.sdlc-v2/pr-template.md`.

## What the skill writes from

- **The commits of the branch.** The skill gets each commit of the branch
  since it left the default branch, oldest first, and covers every one.
  `--base <branch>` does not change this list.
- **The conversation.** The skill uses the files that it edited and what you
  told it in the same session.
- **No diff.** The skill runs no command to read a diff. When the context is
  not sufficient for a section, the skill asks you.
- **OpenSpec, when present.** When `openspec/config.yaml` exists and the
  skill finds one active change, it reads `proposal.md` for Business Context
  and Business Benefits, and `design.md` for Technical Design. It adds the
  line `**OpenSpec:** openspec/changes/<name>/` below the title. When more
  than one change is active and the branch name does not select one, the
  skill skips this step and asks no question.

## Fill rules

- Each section of the active template is always present. The only exception
  is `## JIRA Ticket`, which is absent when the branch name carries no ticket.
- A section gets real content when the context gives it.
- A section gets `N/A`, with a short reason, when it does not apply.
- A section gets `Not detected` when the skill looked and found nothing.
- The skill never invents a Jira ticket, a business reason or a technical
  claim. It asks you a question first, most often for Business Context and
  Business Benefits.
- The title and the body carry no attribution line of an AI tool.

## Quality gates

The skill checks the draft against these gates before you see it. It corrects
each failing gate, with at most 2 rounds for each gate.

| Gate | The draft passes when |
|---|---|
| All sections present | Each section of the active template has real content, `N/A` or `Not detected`. |
| Specificity | The Summary names a concrete change. |
| Business honesty | Business Context and Business Benefits are concrete or `N/A`. No invented reason. |
| No file paths | Changes Overview has zero file paths. With your own template, this gate applies only when the template has a section named "Changes Overview". |
| Title length | The title has fewer than 72 characters. |
| No fabrication | Each claim comes from the context that the skill has. |
| JIRA accuracy | The Jira section is absent when no ticket was found. When present, it holds the detected ticket. |
| Audience check | A reader who is not technical can read the Summary and the business sections. |
| Documentation sync | When the change adds commands, changes structure, renames concepts or adds directories or scripts, the skill asks you to confirm that the docs are up to date. |
| Link verification | Each URL in the body passes the link check below. |

`--auto` and `--skip-approval` do not skip the gates.

## Link check

After your `yes`, or after a skipped publish question, the skill checks each
link in the final body before the publish.

- A link that fails the check stops the run. The skill does not publish.
- The skill shows each failed link with its `url`, `line`, `reason` and
  `detail`.
- The skill does not try again, and it does not change a URL without your
  input.
