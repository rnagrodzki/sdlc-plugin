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

This project's actual "API surface" is not HTTP routes but the MCP tool
handlers in `internal/tools/*.go`, served through `internal/mcpserver`. Count
them with `grep -c 'mcpserver.Register(' $(ls internal/tools/*.go | grep -v _test.go)`
and add up the per-file counts. This
is deliberately a custom dimension, not a generic `api-review`, because the
project's own `.sdlc-v2/config.toml` `plan.guardrails`/`execute.guardrails`
already name this as a first-class concern (e.g.
`mcp-tool-description-precise`, `mcp-output-llm-contract`,
`mcp-error-actionable`, `payload-clarity-for-llms`,
`tool-orchestrates-fast-descriptive-output`). Hold new/changed tools to
those same guardrails:

- Tool descriptions state precisely what the tool does, what it mutates,
  and what it returns — vague descriptions ("handles setup") make it hard
  for the calling model to pick the right tool.
- Tool output is structured for LLM consumption: consistent field names
  across related tools, no ambiguous nulls — a nil or empty collection
  renders as `(none)`, never a blank line — and large or unbounded output
  is paginated or capped rather than dumped raw (see `internal/execx`'s
  `ErrOutputCap` precedent).
- Errors returned by a tool are actionable — they say what went wrong and
  what the caller should do next, not just "failed" or a raw Go error
  string with no context.
- An error caused by a limit or cap (a count of entries, a size bound, a retry
  count) MUST state the limit and the value that exceeded it. 'Too many files'
  without the limit is a finding. This extends the actionable-errors bullet
  above.
- A tool that changes its input/output shape also updates the
  corresponding JSON schema under `plugins/sdlc/schemas/*.schema.json`, and
  any skill `.md` that documents the tool's call signature.
- New tools follow the existing naming convention (`<noun>_<verb>` or
  `<verb>_<noun>`, matching siblings already registered in
  `internal/mcpserver`) rather than introducing an inconsistent scheme.
- Collection fields (arrays, slices) that are nil or empty must render as
  `(none)` at the dispatcher level before the result leaves the plugin.
  Handlers must never leave a blank line or an empty heading for a field
  that logically should contain zero or more items — this ambiguity
  breaks LLM reasoning and violates the 'no ambiguous nulls' contract.
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
- Every key that a handler writes to a persisted state file, and every key
  that a later action reads from that file to decide an action (for example a
  flag that a prune or resume step tests), MUST be declared in the schema or
  tool input description that covers the file and in the format doc that
  describes it. A diff that touches only Go can leave the schema and the doc
  stale, so grep the readers of the file for the keys they read and compare
  that set with the declared set. A key that drives a decision and is not
  declared is a finding. This extends the JSON-schema bullet above, which
  covers input and output shape only.
- Multi-step APIs (append, log entries, stateful updates) that split on a
  field separator (e.g. `learningsRemove` splits entries on `"\n\n"`) must
  validate at entry time that inputs don't contain the separator, and
  reject the write before mutation if they do. Mutation/removal operations
  (delete, remove, unwind) must echo the affected resource or prior state
  in the response (e.g. a `removed`/`prior` field) so callers can verify
  what changed and support undo/recovery workflows.
- When a new sub-feature or action is added to an existing handler with new
  input/output fields, verify: (1) every new input field is explicitly
  validated for type and content in the handler (not silently coerced);
  (2) every generated output value (IDs, timestamps) is echoed in the
  response so the caller can verify what was recorded; (3) the JSON schema
  is updated to document all new `*In` fields with `jsonschema_description`
  tags and closed-set enums where applicable.
- When a handler action writes a state file owned by another tool (a
  cross-tool side effect), verify: (1) the action's description in the
  tool registration names the side effect and its recovery contract; (2)
  any error from the state write (permission denied, disk full, concurrent
  write) surfaces in the tool's output — not dropped, not conflated with
  "file not found"; (3) a not-found condition (the side-effect state file
  does not exist yet) is distinguished from a real read/write error in the
  error handling and output; (4) any reset or modification of
  sibling-owned state is echoed in the tool's response so the caller can
  verify what was affected; (5) the new output field name and type match
  the sibling tools' `*Out` field names and types in the same file (e.g.
  if one tool outputs `Warnings []string`, a parallel output from a
  different action must not use `Warning string`).

- When a narration/message template references a set of enum-like phase or
  state values (e.g. a heartbeat/liveness message's phase list), the
  values used must match the canonical enum definition exactly — flag a
  hand-written free-text placeholder list that can drift from the real
  enum.
- When a handler's actions or output fields change (e.g. a new `fix` field
  is added to an `*Out` struct, or a new action is introduced), the tool's
  registered `Description` (passed to `mcpserver.Register*`) must be
  updated to name the new action's Requires/Optional inputs and every new
  output field by name. A tool description that omits changed or new
  actions/fields is incomplete.
- Values read from config or state files and spliced into generated
  artifacts (workflow YAML, templated code, shell scripts) must be
  validated against the destination syntax before the splice (e.g. a
  GitHub Actions secret name against `^[A-Za-z_][A-Za-z0-9_]*$` with no
  `GITHUB_` prefix). A bad value must fail loud with a `DomainError` that
  names the value and the rule it breaks; it must never reach disk.
- Numeric values parsed from config files (TOML via `config.Read`) or `*In`
  struct fields must be validated for edge cases and platform limits: NaN
  and ±Inf for floats, overflow and underflow for integers, and
  below-minimum semantics (e.g. `maxParallelDimensions` must be >= 1). Each
  validation must reject the invalid value with a `DomainError` that names
  the value, the rule it breaks, and the acceptable range or constraint.
- A JSON-encoded collection input (for example `plannedWavesJson`) MUST
  document its omitted-field behavior and each input the handler rejects (an
  empty array when entries are required, duplicate IDs, partial coverage of
  the set it references) in its `jsonschema_description`. The handler MUST
  reject each one with a `DomainError` that names the broken rule.
- Every write action (record, append, set, skip, create) MUST echo the values
  it wrote in its output. This includes each value the handler generated and
  the caller did not supply, such as a timestamp, an ID, a file name or a
  slug. An output that holds only `ok` and `next`, or that omits a generated
  value, is a finding.
- A no-op or already-done shortcut MUST check the real filesystem or git state
  it claims, not a marker in a header or input file. Example: the
  `openspec_save` 'already' path trusted the plan header.
- A handler that changes git branch or working-tree state, or rewrites a
  user-owned file, MUST do so after every step that can fail, or restore the
  prior state when a later step fails. Example: `openspec_save` switched
  branch before Materialize and left the tree on the new branch.
- A zero count caused by an absent optional input (for example a missing
  `tasks.md`) MUST come with a warning that names the file. Example: a
  missing `tasks.md` reported `refsStamped 0` with no warning.
- A handler that replaces a user-owned file MUST write a temp file and rename
  it, or the review MUST confirm that an in-place write is safe. Example:
  `openspec_save` rewrote the plan file in place.
- **Closed-set reason values.** Each value of a closed-set state or reason
  field MUST name one condition. A catch-all value that merges distinct
  conditions (for example one value for a stalled worker and a worker that
  never checked in) hides the cause and is a finding. Split the value, or add
  a field that carries the distinction.
- **Terminal-write precedence.** When two actions can each write a terminal
  reason for the same record (for example a stop reason, then a checkout), the
  description MUST state which write wins on read. A later write MUST NOT drop
  an earlier stop reason, or change the status a route reads, unless the
  description names that rule.
- A later write to a record MUST keep every field that an earlier write set,
  unless the description names the rule that drops it. A status-only update
  that rebuilds the row from its own inputs loses linked fields such as a
  stored ID. Compare the fields the update copies from the old row with the
  fields the row can hold, and flag each field that the update drops. This
  generalizes the terminal-write precedence bullet above, which names only the
  stop reason.
- A warning that reports a skipped, ignored or replaced input (for example an
  unreadable timestamp or an unknown key) MUST name the value or field, the
  expected format with one example, and the effect on the result, such as the
  value used instead, a row left out or a field shown blank. A warning that
  only says the input was invalid is a finding. This extends the
  actionable-errors bullet above to warnings.

## Cross-references

- `mcp-contract-compliance.md` covers the specific structural tag/field
  contracts (`jsonschema_description`, `enum`, `Next`, `Suggestion`)
  introduced by the MCP Tooling Hardening plan — more granular than the
  general quality rules above.
- `skill-wiring-consistency.md` covers SKILL.md-to-tool parameter name
  alignment, a distinct concern from tool-internal contract quality.
