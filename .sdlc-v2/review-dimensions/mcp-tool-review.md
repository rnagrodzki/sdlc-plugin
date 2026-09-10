---
name: mcp-tool-review
description: MCP tool contract quality for the 27 handlers under internal/tools — precise descriptions, LLM-consumable output, actionable errors, per this project's own guardrails.
triggers:
  - "internal/tools/**"
  - "internal/mcpserver/**"
  - "plugins/sdlc/schemas/**"
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

## Cross-references

- `mcp-contract-compliance.md` covers the specific structural tag/field
  contracts (`jsonschema_description`, `enum`, `Next`, `Suggestion`)
  introduced by the MCP Tooling Hardening plan — more granular than the
  general quality rules above.
- `skill-wiring-consistency.md` covers SKILL.md-to-tool parameter name
  alignment, a distinct concern from tool-internal contract quality.
