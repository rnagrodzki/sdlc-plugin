# Description templates

Read this page when you want the descriptions of your Jira issues to follow
your own structure, or when you want to know the rules that the skill applies
to each description and comment. The short overview is
[/sdlc:jira](../jira.md).

## Example

```text
/sdlc:jira
Create a task in PROJ: "Add rate limiting to auth API"
```

When the issue type `Task` has no template, the skill prints this notice and
continues:

```text
No template for Task — drafting the description from the base structure (Summary / Context / Acceptance Criteria). To use your own, create .sdlc-v2/jira-templates/Task.md
```

To use your own structure, create that file. The skill fills it the next
time it writes a description for a `Task`.

## Where a template comes from

A template is a markdown file with the name of the issue type: `<Type>.md`.
The skill uses the first source that has one:

1. Your template at `.sdlc-v2/jira-templates/<Type>.md`. It always wins.
2. A default template. The `jira` tool looks for `<Type>.md` in the first of
   these directories: the `templatesDir` input of the tool,
   `$CLAUDE_PLUGIN_ROOT/skills/jira/templates` when it exists, the first
   `jira/templates` directory under `~/.claude/plugins`, and
   `<cwd>/plugins/sdlc/skills/jira/templates`.
3. The default template of the parent type, for three subtask types:
   `Sub-bug` uses `Bug`, and `Sub-task` and `Subtask` use `Task`. The skill prints
   `Using <fallbackTo> template for <type> — override at .sdlc-v2/jira-templates/<type>.md`.
4. The base structure below.

The plugin ships no default templates today. Unless one of the directories of
source 2 has templates, only your own templates apply.

## The base structure

```markdown
## Summary
- {what_and_why}

## Context
- {background}

## Acceptance Criteria
- [ ] {criterion}
```

The skill fills the base structure in the same way as a template. It repeats
a bullet when it needs more than one. It removes `## Context` when it has
nothing real to put there. It adds no other `## ` section. The critique, the
approval and the link check still run.

## Copy the default templates

```text
/sdlc:jira --init-templates
```

The skill copies the default templates to `.sdlc-v2/jira-templates/`, reports
how many it copied and how many it skipped because the file exists, then
stops. It runs no Jira operation.

- When it finds no default templates, it copies nothing and says so. This is
  the result today, because the plugin ships none.
- When an issue type of the cached project has no default template with the
  same name, the skill asks which default template to copy for that type. It
  suggests `Epic` or `Task` from the hierarchy level of the type, and you can
  skip the type.

## Placeholders

A template marks the text to fill with `{name}` or `[bracketed prose]`.

- The skill fills a marker from your request or from a value in the cache.
- The skill asks you about each marker that it cannot fill from those two
  sources. It does not fill a marker from a guess.
- The skill never sends a raw placeholder to Jira. It replaces the marker
  with real content or removes the line or the section.

## Rules for a description

The critique checks each description against these rules and shows each
violation in the `Critique:` block.

| Part | Rule |
|---|---|
| Sections | Each `## ` heading is a heading of the template or of the base structure. The skill invents no section. |
| Section body | A bullet list, a numbered list or a set of sub-headings. No paragraph is longer than two lines. |
| `## Acceptance Criteria` | Only `- [ ]` checklist items. No sentence before or after them. |
| `## Release Notes` | One sentence is permitted. Two or more sentences fail the check. |
| Summary | An imperative phrase of 100 characters or fewer. |
| Filler | No sentence such as "This ticket covers…" or "In summary…". |

## Markdown that Jira shows correctly

The skill writes descriptions and comments in markdown. It sends a
description as markdown. It converts a comment to the Atlassian Document
Format (ADF) before it posts the comment.

| Use | Do not use |
|---|---|
| Headings of level 1 to 3 | HTML tags such as `<b>`, `<br>` or `<details>` |
| Bold, italic and inline code | Nested blockquotes (`>>`) |
| Unordered and ordered lists | Footnotes and definition lists |
| Fenced code blocks | Strikethrough (`~~text~~`) |
| Links, tables and horizontal rules | Custom emoji (`:smile:`) |
| Blockquotes of one level | Raw HTML entities such as `&nbsp;` |

A comment must not contain a task list (`- [ ]`): many Jira versions show it
as literal text.
