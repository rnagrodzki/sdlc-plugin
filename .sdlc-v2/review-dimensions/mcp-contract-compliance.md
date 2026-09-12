---
name: mcp-contract-compliance
description: Validates MCP tool structs follow structural contracts — *In fields have jsonschema_description tags stating plain-text vs JSON-encoded input; closed sets have enum tags; *Out structs have Next; errors populate Suggestion.
triggers:
  - "internal/tools/**"
  - "internal/mcpserver/**"
severity: high
---

Review changed MCP tool structs for structural contract compliance.
These rules codify the patterns established by the MCP Tooling Hardening
plan and enforced by the `mcp-parameter-documented`, `mcp-error-has-suggestion`,
and `mcp-output-drives-behavior` guardrails.

## Input struct contracts (`*In`)

- Every exported field on an `*In` struct MUST have a `jsonschema_description:"..."`
  struct tag. The description should explain what the field does, when to set it,
  and what happens when omitted (for `omitempty` fields).
- Fields representing a closed set (validated by a `switch` statement or
  constant block in the handler) MUST additionally have a
  `jsonschema:"enum=val1,enum=val2"` tag listing all accepted values.
  Do NOT add enum tags to open-ended fields — only genuinely closed sets.
- Examples of closed sets in this codebase:
  - `releaseLevel`: major, minor, patch (validated at pr.go switch)
  - `releaseSource`: user, config, pipeline (validated at pr.go switch)
  - `quality`: full, balanced, minimal (ShipPrepareIn)
- Examples of open sets (do NOT enum):
  - `rebase`: ship SKILL.md says other strings are "informational"
  - `title`, `body`, `branch`: free-form text
- Handlers MUST validate input shapes and reject invalid schemas loudly
  before processing, rather than silently producing degraded output.

## Output struct contracts (`*Out`)

- Every `*Out` struct that serves as a tool's primary output MUST have a
  `Next string json:"next"` field — the struct tag MUST NOT include
  `omitempty`. The field must appear in every response JSON. Empty string
  `""` means "no next step"; an absent field violates the contract.
- `Next` must be populated with an exact per-outcome string, not free-form
  interpolation. Pattern: `VersionPrepareOut.Next` at version.go.
- Tool handlers registering via `Register[TIn,TOut]` MUST call
  `WithOutputSchema[TOut]` to expose the concrete output structure to the
  LLM. Omitting `WithOutputSchema` hides the output shape from the model.
- Handler signatures MUST return concrete `*Out` types, never `any` or
  `interface{}`. Type erasure prevents schema generation and forces the
  LLM to guess the output shape.
- Collections (slices, maps) must be initialized as empty `[]T` or
  `map[K]V{}`, never nil. Nil collections serialize to JSON null;
  normalize at the dispatcher level before returning output.

## Error contracts

- When a handler returns a `DomainError`, `InfraError`, or `DataError` with
  a recoverable condition (the caller can do something to fix it), the
  `Suggestion` field MUST be populated with a specific recovery instruction.
- Non-recoverable errors (unexpected panics, marshal failures) should still
  populate `Suggestion` (with a generic message or empty string), not rely
  on `omitempty` to hide it.
- The `Suggestion` field MUST NOT have `omitempty` on the struct tag —
  every error response must include the `"suggestion"` field in JSON,
  empty string if no specific recovery applies, but never omitted.

## Cross-references

- General MCP tool quality rules are in `mcp-tool-review.md` — this
  dimension is specifically about structural tag/field contracts.
- Guardrails: `mcp-parameter-documented` (plan), `mcp-error-has-suggestion`
  (plan), `mcp-output-drives-behavior` (plan), `mcp-schema-tags-required`
  (execute).
