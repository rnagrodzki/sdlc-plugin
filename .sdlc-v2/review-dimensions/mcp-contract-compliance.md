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
- A closed-set field's `jsonschema_description` MUST list every allowed value.
  A description that calls the field plain text or free text is a finding,
  even when the field has an enum tag.

## Output struct contracts (`*Out`)

- Every `*Out` struct that serves as a tool's primary output MUST have a
  root `Next string json:"next"` field, which the renderer hoists to the
  result's `**Next:**` line. A genuinely terminal tool may omit it, but its
  tool description must say so.
- `Next` must be populated with an exact per-outcome string, not free-form
  interpolation. Pattern: `VersionPrepareOut.Next` at version.go.
- Tools publish no output schema. The renderer in `internal/mcpserver`
  shows the LLM the concrete shape by rendering every field with its JSON
  key as the label, so the output shape is visible in the result itself.
  Verify: `grep -rn 'OutputSchema' internal/ --include='*.go'` returns no
  non-test hit.
- Handler signatures SHOULD return a concrete `*Out` type. Three tools
  return `any` across dozens of actions: `execute_state`
  (`internal/tools/execute_state.go`), `ship_state`
  (`internal/tools/ship_state.go`) and `jira` (`internal/tools/jira.go`).
  The reflection renderer walks those too, so `any` is a readability cost,
  not a contract violation. A new tool with a single output shape has no
  reason to use it. (`poll_await` is NOT one of them — it returns a
  concrete `stepper.Envelope`.) Verify — expect exactly 3 hits:
  `grep -rn 'mcpserver.Ctx, in [A-Za-z]*) (any, error)' internal/tools/*.go`
- A nil or empty collection must render as `(none)`. The renderer does
  this at the dispatcher level (`renderNone` in
  `internal/mcpserver/render.go`), so handlers do not have to
  pre-initialize slices, and no result may show a blank line or an empty
  heading where a collection was.
- `omitempty` wins over `(none)`. Rule 14 in `docs/mcp-output-contract.md`
  runs first, when the renderer collects a struct's fields: a field tagged
  `omitempty` or `omitzero` with an empty value is left out completely. Only
  an untagged field renders `- <key>: (none)`. So "absent" in a skill or doc
  means the field carries `omitempty`, and "`(none)`" means it does not.
  Decide per field: drop `omitempty` when the reader must see that the value
  is empty (for example `ShipVerifySideEffectOut.Expected`). When a doc says
  a field is absent, check its tag with `grep -n '<jsonKey>' internal/tools/*.go`.

## Error contracts

- When a handler returns a `DomainError`, `InfraError`, or `DataError` with
  a recoverable condition (the caller can do something to fix it), the
  `Suggestion` field MUST be populated with a specific recovery instruction.
- A no-op or empty outcome (for example 'nothing to promote', 'already up
  to date', 'no changes') is recoverable by definition. Its handler MUST
  return a typed error with a specific `Suggestion`, or a success `*Out`
  with a specific `Next`, that states what makes the operation applicable
  or how to confirm the work is done. An untyped `errors.New` or
  `fmt.Errorf` return for this outcome, or reliance on the
  `defaultRecovery` fallback, is a finding.
- A `Suggestion` MUST name a recovery action the caller can perform. The named
  tool or action MUST accept the input that failed and MUST be able to change
  the failing condition. A `Suggestion` that sends the caller to a tool that
  cannot fix the error is a finding.

Every error result MUST render a non-empty `## Do this` section. When a typed
error carries no `Suggestion`, `defaultRecovery(code)` supplies one; a rendered
error with an empty or missing `## Do this` section is a defect. Both live in
`internal/mcpserver/envelope.go` (`defaultRecovery`, called from `renderError`).

## Handler input validation

- When a handler receives new fields in an `*In` struct, it must explicitly
  validate each field's type and value before using it. Silent type
  coercion (e.g. accepting a string when an int is expected, silently
  falling back to a default value) is a defect. Handlers must fail-loud:
  validate or reject, never silently coerce. For example, if a field is
  expected to be one of a known set of values, the handler must check it
  against that set and reject unknown values with a `DomainError`, not
  silently fall back to a default.

## Output field conventions for []string

- A `[]string` output field carrying message content holds one message per
  array element. When a handler populates such a field with both an error
  message and a recovery suggestion from the same condition, each goes in a
  separate array element — do not splice them into one string. Example:
  `Errors: {mkErr.Error(), mk.Suggestion()}` for two elements, not
  `Errors: {mkErr.Error() + "\n" + mk.Suggestion()}` for one.

## Parameter contract versioning

- When an existing `*In` struct parameter gains a second effect or behavior
  (e.g., a `SkipConfigCheck` flag that previously only skipped preflight
  checks now also skips a new moved-keys migration), its
  `jsonschema_description` tag MUST be updated to document all effects. A
  parameter whose description is stale relative to its handler code is a
  contract violation because the LLM cannot determine when to set the flag
  based on incomplete documentation.
- Handlers must re-verify that all callers of the updated parameter
  understand its new behavior, and that code paths using the flag account
  for both effects.

## Output field contract versioning

- When an existing `*Out` struct field gains a second source or case (e.g.,
  an `Errors` field populated by a new code path, or a `Sources` map that
  now includes a value from a new config file), its `jsonschema_description`
  tag MUST be updated to document all sources/cases. An output field whose
  description is stale relative to the handler code is a contract violation
  because the LLM cannot determine the field's reliability or provenance.
- When a handler gains a new filesystem side effect or reads a new config
  source, the handler's registered tool/action description (passed to
  `RegisterTools` in `internal/mcpserver`) MUST be re-read and audited for
  claims now falsified by the new behavior. Example: a handler description
  claiming 'Stateless' or 'read-only' must be revised if the handler now
  writes files or reads a new config source.
- When an output field holds provenance/source labels (e.g., a `Sources` map
  whose values indicate where a setting came from), and a handler now reads
  from a new file or location, the provenance enum must be re-verified:
  (1) the label still accurately names the source;
  (2) any SKILL.md or sibling tool that gates behavior on the label
     (`if sources.X == "config"`) is checked to confirm the new behavior is
     semantically compatible with the gate.

## Review procedure for closed-set enum tags and error-field coverage

When reviewing changes to MCP handler code, perform these concrete checks:

- **Enum-tag completeness:** For each modified handler function, identify every `switch` statement that validates a string field from an `*In` struct. Cross-reference each switched field against the struct definition and confirm it carries a `jsonschema enum=val1,enum=val2,...` tag matching the case labels. A switch on `Action` with cases "plan_format", "discovery", etc. must have `jsonschema enum=plan_format,enum=discovery,...` on the Action field.
- **Error field coverage (Suggestion):** List every point in the handler where a `DomainError`, `InfraError`, or `DataError` is instantiated (including default/fallthrough cases in switch statements). Verify each one populates the `Suggestion` field with a recovery instruction. This is a common miss in default cases — confirm they explicitly set Suggestion, not silently omit it.

## JSON-encoded string fields

- An `*In` field whose Go type is `string` but whose handler parses it via
  `json.Unmarshal` MUST have a `jsonschema_description` that explicitly
  states the JSON-encoding requirement, with an example value. A
  description that omits this (or says "free-text") while the handler
  requires JSON is a contract violation.
- For task-done/wave-done inputs: `filesAdded` must be a documented subset
  of `filesChanged`, and status fields must use the tool's declared enum
  (e.g. `DONE_WITH_CONCERNS` distinct from `DONE`) — flag any invented or
  conflated status string.

## Annotation reason accuracy

- A tool annotation (read-only, idempotent) with a reason string must be
  checked against an actual call-graph/reference search of the handler —
  a plausible-sounding reason is not evidence the annotation is correct.

## Cross-references

- General MCP tool quality rules are in `mcp-tool-review.md` — this
  dimension is specifically about structural tag/field contracts.
- Guardrails: `mcp-parameter-documented` (plan), `mcp-error-has-suggestion`
  (plan), `mcp-output-drives-behavior` (plan), `mcp-schema-tags-required`
  (execute).
