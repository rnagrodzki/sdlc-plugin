---
name: mcp-contract-compliance
description: Validates that MCP tool structs follow the structural contracts — *In fields have jsonschema_description tags, closed sets have enum tags, *Out structs have Next field, and error paths populate Suggestion.
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

## Output struct contracts (`*Out`)

- Every `*Out` struct that serves as a tool's primary output MUST have a
  `Next string json:"next"` field (no `omitempty`).
- `Next` must be populated with an exact per-outcome string, not free-form
  interpolation. Pattern: `VersionPrepareOut.Next` at version.go.
- Empty string `""` is valid and means "no next step" — this is different
  from omitting the field entirely.

## Error contracts

- When a handler returns a `DomainError`, `InfraError`, or `DataError` with
  a recoverable condition (the caller can do something to fix it), the
  `Suggestion` field MUST be populated with a specific recovery instruction.
- Non-recoverable errors (unexpected panics, marshal failures) may leave
  `Suggestion` empty.
- The `Suggestion` field appears in the error envelope JSON as
  `"suggestion"` (omitted when empty via `omitempty`).

## Cross-references

- General MCP tool quality rules are in `mcp-tool-review.md` — this
  dimension is specifically about structural tag/field contracts.
- Guardrails: `mcp-parameter-documented` (plan), `mcp-error-has-suggestion`
  (plan), `mcp-output-drives-behavior` (plan), `mcp-schema-tags-required`
  (execute).
