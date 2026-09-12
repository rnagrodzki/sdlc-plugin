---
name: mcp-tool-review
description: MCP tool contract quality for handlers under internal/tools — precise input-shape and encoding descriptions, LLM-consumable output, actionable errors, and fail-loud validation of input shape violations.
triggers:
  - "internal/tools/**"
  - "internal/mcpserver/**"
  - "plugins/sdlc/schemas/**"
  - "internal/setupmeta/**"
severity: high
---

This project's actual "API surface" is not HTTP routes but 27 MCP tool
handlers in `internal/tools/*.go`, served through `internal/mcpserver`. This
is deliberately a custom dimension, not a generic `api-review`, because the
project's own `.sdlc-v2/config.json` `plan.guardrails`/`execute.guardrails`
already name this as a first-class concern (e.g.
`mcp-tool-description-precise`, `mcp-output-llm-contract`,
`mcp-error-actionable`, `payload-clarity-for-llms`,
`tool-orchestrates-fast-descriptive-output`). Hold new/changed tools to
those same guardrails:

- Tool descriptions state precisely what the tool does, what it mutates,
  and what it returns — vague descriptions ("handles setup") make it hard
  for the calling model to pick the right tool.
- Tool output is structured for LLM consumption: consistent field names
  across related tools, no ambiguous nulls where an empty array/object
  would be clearer, and large or unbounded output is paginated or capped
  rather than dumped raw (see `internal/execx`'s `ErrOutputCap` precedent).
- Errors returned by a tool are actionable — they say what went wrong and
  what the caller should do next, not just "failed" or a raw Go error
  string with no context.
- A tool that changes its input/output shape also updates the
  corresponding JSON schema under `plugins/sdlc/schemas/*.schema.json`, and
  any skill `.md` that documents the tool's call signature.
- New tools follow the existing naming convention (`<noun>_<verb>` or
  `<verb>_<noun>`, matching siblings already registered in
  `internal/mcpserver`) rather than introducing an inconsistent scheme.
- Collection fields (arrays, slices) must normalize nil to empty
  array/object at the dispatcher level before returning tool output.
  Handlers must never serialize null for fields that logically should
  contain zero or more items — this ambiguity breaks LLM reasoning and
  violates the 'no ambiguous nulls' contract.
- Descriptor/doc strings naming filesystem paths, extensions, or glob
  patterns must be verified against the actual implementation (grep the
  loader for `paths.DataDir`, `filepath.Join`, `ReadDir`,
  `strings.HasSuffix`) rather than trusted at face value.
- Tool descriptions must state precisely what the tool does, what it
  mutates, what it returns, AND (for multi-action tools) what inputs are
  required vs optional for each action — following the `execute_state.go`
  convention of "Requires: ... Optional: ..." at registration time. Vague
  descriptions or missing per-action field semantics make it hard for the
  calling model to pick the right tool and supply correct parameters.
- A tool that changes its input/output shape must update the corresponding
  JSON schema under `plugins/sdlc/schemas/*.schema.json`, AND proactively
  validate that marshaled Go struct instances conform to the schema (every
  field the Go struct writes must be accepted by the schema). Schema-code
  drift — schemas rejecting fields that code writes, or vice versa —
  breaks downstream tool consumption.
- Multi-step APIs (append, log entries, stateful updates) that split on a
  field separator (e.g. `learningsRemove` splits entries on `"\n\n"`) must
  validate at entry time that inputs don't contain the separator, and
  reject the write before mutation if they do. Mutation/removal operations
  (delete, remove, unwind) must echo the affected resource or prior state
  in the response (e.g. a `removed`/`prior` field) so callers can verify
  what changed and support undo/recovery workflows.

## Cross-references

- `mcp-contract-compliance.md` covers the specific structural tag/field
  contracts (`jsonschema_description`, `enum`, `Next`, `Suggestion`)
  introduced by the MCP Tooling Hardening plan — more granular than the
  general quality rules above.
- `skill-wiring-consistency.md` covers SKILL.md-to-tool parameter name
  alignment, a distinct concern from tool-internal contract quality.
