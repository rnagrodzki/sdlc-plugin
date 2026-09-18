# MCP Output Contract

Every MCP tool result is rendered as plain Markdown text in `content[0].text`. There is no JSON
envelope, no `structuredContent`, and no `outputSchema` — a tool's return value (a `*Out` struct, a
pointer to one, or a bare `map[string]any` for the four tools that have no struct) is walked by
reflection and turned into Markdown by one shared renderer. Every tool's output goes through the same
code, so there are 2 shapes total (success, error), not one per tool.

Source of truth:

- `internal/mcpserver/render.go` — the success renderer (`renderOK`) and its reflection walker.
- `internal/mcpserver/envelope.go` — error classification (`mapError`, the `DomainError` /
  `InfraError` / `DataError` types) and the error renderer (`renderError`, `defaultRecovery`).
- `internal/mcpserver/register.go` — wires both into the five exit paths of every registered tool's
  handler.

## First line

Every rendered result — success or error — starts with exactly one line:

```
^# ([a-z_]+) — (?:(ok)|error \((domain|infra|data)\))$
```

That is `# <tool> — ok` for success, or `# <tool> — error (<domain|infra|data>)` for a failure. The
tool name is the registered tool name (`[a-z_]+`); the error variant always names one of the three
codes. This line is machine-checkable and is what tests assert on — nothing else in the contract is
positionally fixed the way this line is.

## Success shape

A success result can carry four fixed pieces, always in this order, each present only if the tool's
output has something to put there:

1. A `**Next:**` line — not a heading, just one bold-prefixed line.
2. `## Summary` — a section.
3. `## Warnings` — a section, rendered as a bullet list.
4. `## Fields` — a section, rendered as a bullet list of the remaining root-level scalars.

After those, every remaining root-level struct, map, or slice-of-struct/map field gets its own
section (`## <key>`, nested to `### <parent>.<key>`, and so on), in the order the fields appear on
the struct (or sorted-key order for a map).

````markdown
# plan_prepare — ok

**Next:** call plan_mark with marker="skillInvoked"

## Summary
Template resolved. 44 guardrails loaded.

## Warnings
- config.toml uses a legacy key: plan.reviewers

## Fields
- activeTemplatePath: /Users/rafal/.../plan-template-default.md
- fromOpenspec: (none)

## template
- headerMarkdown:
```
# My Plan
```

### template.routing
- pipelineMode: full
- fileCount: 12

## guardrails[0]
- name: kiss
- severity: error

## guardrails[1]
- name: yagni
- severity: warning
````

Note the fenced `headerMarkdown` block: it sits at column 0, not indented under its bullet, and
carries no language tag — see rule 9 below.

## The walker rules

`render.go` walks the output value by reflection. These are its 15 rules, in the precedence order the
source states them:

1. A root string field, or a root map key, named `next` becomes the `**Next:**` line, and is omitted
   from `## Fields`. The hoist fires only at the root and only for a non-empty string — a nested
   `next` (for example a `*NextAction` object, or a `next` two levels deep) renders in place instead.
2. A root string field named `summary` becomes the `## Summary` section.
3. A root `[]string` field named `warnings` becomes the `## Warnings` bullet list.
4. Every remaining root scalar becomes a `## Fields` bullet, using the field's JSON key verbatim (not
   a humanized label — `pipelineMode`, not `Pipeline mode`).
5. A struct or map field becomes its own section: `## <key>` at the root, `### <parent>.<key>` one
   level down, and so on. The heading level is capped at 6 (Markdown has no `#######`).
6. A `[]struct` (or slice of maps) becomes sibling sections with the index in the heading —
   `## <key>[0]`, `## <key>[1]`, … — not nested children of one `## <key>` section.
7. A `[]scalar` (e.g. `[]string`, `[]int`) becomes an indented bullet list under its key.
8. An empty collection renders as `(none)`. This fires on a nil **or** `len == 0` slice or map, on a
   nil pointer, and on an empty string — nil and empty are indistinguishable to the reader and always
   render the same way. A field that would be a bullet renders `- <key>: (none)`; a field that owns a
   section keeps its heading and has `(none)` as its whole body.
9. A string containing `\n` is written inside a fenced code block. The fence length is one more
   backtick than the longest backtick run already in the content, with a minimum of 3 — long enough
   that the content can never break out of its own fence. The fence carries no language tag, and it
   is written at column 0, not indented under the bullet that introduces it.
10. A field tagged `` render:"raw" `` is emitted verbatim, never inside a fence, even if it contains
    `\n` or backticks (see [`render:"raw"`](#renderraw) below). Like a fenced block, verbatim content
    is written at column 0, regardless of how deeply its bullet is indented.
11. A map's keys are sorted with `sort.Strings` before rendering, so field order is stable across
    calls instead of following Go's randomized map iteration.
12. A subtree deeper than `renderMaxDepth` (6), or a cycle (a pointer or map already on the current
    walk path), stops emitting headings and flattens to `- dotted.path: value` bullets instead. A
    cycle renders `(cycle)` and stops there.
13. A non-nil pointer is dereferenced and rendered by these same rules — a `*T` never prints as a Go
    pointer address. A nil pointer is covered by rule 8.
14. A field whose JSON tag carries `omitempty` (or `omitzero`) and whose value is the zero value is
    omitted entirely, reproducing what `encoding/json` does today for the same struct.
15. An `any` element inside a `[]any` or `map[string]any` is unwrapped to its dynamic value, then
    rendered by these same rules.

## Error shape

```markdown
# commit_prepare — error (domain)

## What happened
No staged changes found. `git diff --cached --name-only` returned no files in
/Users/rafal/repositories/sdlc-plugin.

## Do this
Stage the files you want to commit with `git add <path>`, then call commit_prepare again.
```

An error result has exactly two sections after the first line:

- `## What happened` — the error message (`msg`). If `msg` is empty, this renders `(none)`.
- `## Do this` — recovery guidance. **This section is never empty.** If the error carries its own
  `Suggestion` text, that is what renders. If it does not, `renderError` falls back to
  `defaultRecovery(code)`.

### Error codes

`internal/mcpserver/envelope.go` defines three error types, each mapped to one code by `mapError` via
`errors.As`:

| Go type | code | meaning |
|---|---|---|
| `DomainError` | `domain` | a business-logic violation — bad input from the caller's perspective |
| `InfraError` | `infra` | an infrastructure failure — network, filesystem, process |
| `DataError` | `data` | a data-layer problem — schema mismatch, parse failure |

An error that is none of these three types (an unwrapped, unrecognized `error`) is classified `infra`
with no suggestion. `register.go` also renders two error results directly, without going through a
handler's returned error: a recovered panic renders as `infra`, and a failure to unmarshal the tool's
input JSON renders as `data`.

### `defaultRecovery` table

Used by `renderError` whenever the error itself supplied no `Suggestion`:

| code | default `## Do this` text |
|---|---|
| `domain` | "Check the input against this tool's documented parameters and retry with a corrected value." |
| `data` | "The underlying data may be missing, malformed, or stale. Inspect the referenced file or record, regenerate it if needed, and retry." |
| anything else (including `infra`) | "This looks like an environment or infrastructure failure (filesystem, network, or process). Check that the underlying system is reachable and retry." |

The third row is also the fallback for a code the function does not recognize — an unclassified
failure is treated as more likely a plumbing problem than a caller mistake.

## `render:"raw"`

A struct field tagged `` render:"raw" `` is emitted verbatim: no code fence, regardless of whether its
content has newlines or backticks. Only struct fields carry Go struct tags, so a map entry can never
be `raw`.

Use this tag for a value that is already Markdown meant to be shown as-is — for example
`pipeline.Narration.Display`, which skills render verbatim. It is not for arbitrary multiline text;
anything else containing a newline (a `git diff`, a log excerpt) goes through rule 9's fence instead,
so it stays visibly delimited and cannot merge into the surrounding document.

## See also

- `internal/mcpserver/render_test.go` — golden fixtures for the walker rules above.
- `internal/mcpserver/envelope_test.go` — `mapError` and `defaultRecovery` tests.
- `docs/mcp-tool-annotations.md` — the annotation contract (`ReadOnly`, `Destructive`, `Idempotent`,
  `OpenWorld`) that sits alongside this output contract on every registered tool.
