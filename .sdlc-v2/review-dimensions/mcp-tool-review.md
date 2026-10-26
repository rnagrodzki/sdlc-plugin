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
- A new action that reads or writes a resource that another action of the
  same tool, or a sibling tool, already handles (a stored file, a linked
  folder, a config section) MUST apply the same input policy as that action.
  Compare how each action treats a missing path, a directory, a symlink, a
  file over the size cap and a value of the wrong shape, and compare the
  error class (`DomainError`, `InfraError`, `DataError`) that each state
  returns. A state that one action accepts and the other refuses, or two
  error classes for one state, is a finding unless the description of the new
  action names the difference and the reason. This extends the new-action
  bullet above, which checks the new fields and not the policy shared with a
  sibling action.
- A write or repair action that leaves a file on disk MUST leave a file that
  the sibling read or check action of the same resource accepts. Run the read
  action on the file that the write produced. When the read action refuses
  it, the write is a finding, unless the description of the write names the
  refusal and the reason. A write that adds, rewrites or repairs content (a
  heading, a closing fence line, a marker) can create a state that the reader
  rejects, so compare the reader's rules with what the writer emits. A test
  runs the write and then the read on the result, and asserts the read
  outcome. This extends the shared-input-policy bullet above, which compares
  how two actions treat the same input, not the output of one action as the
  input of the other.
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
  the value, the rule it breaks, and the acceptable range or constraint. A
  count or size input needs a maximum as well as a minimum when the handler
  writes the value into a file or uses it to size a loop, a read or an
  allocation. The handler rejects a value above one named maximum constant
  with a `DomainError` that states the maximum and the value, and the
  description of the input builds its number from that constant. An input
  with a minimum and no maximum is a finding.
- A JSON-encoded collection input (for example `plannedWavesJson`) MUST
  document its omitted-field behavior and each input the handler rejects (an
  empty array when entries are required, duplicate IDs, partial coverage of
  the set it references) in its `jsonschema_description`. The handler MUST
  reject each one with a `DomainError` that names the broken rule.
- A guard that decides whether a write may go ahead (a data-loss check, a
  refusal of an unsafe file shape, a check of the ids a write would drop) MUST
  fail closed. A value of an unexpected type or shape (a non-object id, an
  array where a table is expected) MUST refuse the write with a `DomainError`
  when the caller's input is wrong, or a `DataError` when the stored file is
  wrong. The error names the value and the rule it breaks. A guard that reads
  such a value as empty or as 'nothing to protect', through a type assertion
  that drops `ok`, a default branch or a zero value, is a finding, because the
  write then goes ahead and can discard stored data. A test drives each shape
  and asserts that the write is refused and that the file is unchanged. This
  extends the bullets above that reject bad input before a write: they cover
  caller input, not the unknown-shape branch of a safety check.
- Every write action (record, append, set, skip, create) MUST echo the values
  it wrote in its output. This includes each value the handler generated and
  the caller did not supply, such as a timestamp, an ID, a file name or a
  slug. An output that holds only `ok` and `next`, or that omits a generated
  value, is a finding.
- A write action that creates or replaces a file (for example render, write
  or overwrite) is a write action under the echo bullet above, even when it
  names no record. Its output states whether the file was created, replaced or
  left unchanged, and echoes each marker or header line the handler wrote into
  the file. An output that gives only the path and a count, or that does not
  say whether an existing file was replaced, is a finding. This extends the
  write-action echo bullet above, whose verb list names record, append, set,
  skip and create only.
- A no-op or already-done shortcut MUST check the real filesystem or git state
  it claims, not a marker in a header or input file. Example: the
  `openspec_save` 'already' path trusted the plan header.
- A handler that changes git branch or working-tree state, or rewrites a
  user-owned file, MUST do so after every step that can fail, or restore the
  prior state when a later step fails. Example: `openspec_save` switched
  branch before Materialize and left the tree on the new branch.
- A zero count caused by an absent optional input (for example a missing
  `tasks.md`) MUST come with a warning that names the file. Example: a
  missing `tasks.md` reported `refsStamped 0` with no warning. The same holds
  for a zero or short count caused by a wrong-shaped input that a loader the
  handler calls drops with no error (for example a list that holds a
  non-table element). Read the loader and list what it returns for a missing
  key, a wrong type and a wrong-typed element. The handler MUST surface a
  warning or an error that names the key and the shape. A count reported as a
  real result when the loader defaulted it is a finding. The same applies to a
  parser or check that the change adds. A check whose item set is empty (a file
  with no `Task` heading, a list with no entries) MUST report 'nothing
  checked' with a warning that names the missing part, never clean or pass. A
  parser that skips an entry under a known heading because the entry matches no
  rule (for example a bullet in a `Files` list) MUST count the skip and report
  it in a warning that names the entry, or fail with an error that names it. A
  parser that drops an entry with no signal is a finding. A test drives an
  input with no items and an input with one unmatched entry, and asserts the
  warning or the error.
- A parser or check that classifies the lines of a Markdown file (fences,
  table rows, headings, line ends) MUST read every spelling of a construct
  that the file can hold, or report the form it cannot read. Drive these
  through the check and compare each with the plain form: a code fence with
  no closing line, a table row or heading with leading spaces, and CRLF line
  ends. A form that makes a section read as empty, or that the check skips
  with no signal, is a finding: the caller sees 'empty' for a file that has
  content. A test per form asserts the result or the warning. This extends
  the zero-count bullet above, which tests an input with no items and one
  unmatched entry, but not a variant spelling of an entry that is present.
  Example: an unclosed code fence made a check report sections as empty, and
  an indented table row was skipped.
- A handler that replaces a user-owned file MUST write a temp file and rename
  it, or the review MUST confirm that an in-place write is safe. Example:
  `openspec_save` rewrote the plan file in place.
- A handler that replaces an existing user-owned file with a temp file and a
  rename MUST give the new file the mode of the old file. A temp file from
  `os.CreateTemp` has mode 0600, so a rename over a 0644 file narrows the
  mode with no signal. Read the old mode (`os.Stat`) and apply it before the
  rename, or the review MUST confirm that the default mode is right. A test
  creates the file with a mode other than 0600, runs the real handler and
  asserts the mode after the rewrite. A rewrite that changes the mode with no
  stated reason is a finding. This extends the temp-and-rename bullet above,
  which checks only that the write is atomic. Example: a repair of a topic
  file that dropped it from 0644 to 0600.
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
  actionable-errors bullet above to warnings. Three more checks apply. A
  warning for a value of the wrong type names the type found, the type
  expected and the fix. A warning for an exceeded limit or cap states the
  limit, the value that exceeded it and the item dropped, as the limit bullet
  above requires for errors. Read the code that runs after the warning: the
  effect that the warning states MUST be the effect that happens, and a
  warning that says a step is skipped while the code keeps it is a finding.
  Example: a 'Step skipped' warning on a step that the code kept.
- A cap on the number of stored entries, or on the size of a stored value,
  MUST hold on every path that writes the value and on every path that reads
  it to act on it. A cap that only the write path enforces lets a hand-edited
  or older file exceed it with no signal; a cap that only the read path
  enforces lets a bad write through. Grep each writer and each reader of the
  stored value for the cap constant. A path that does not enforce the cap MUST
  warn when the stored data is over the cap, or the description MUST say why
  it trusts the writer. A cap enforced on one path only is a finding. This
  extends the maximum-value bullet above, which covers handler input only.
  Example: a step-time list capped on write and read back with no cap.
- A number that a registered tool or action description states, such as a
  limit, a cap or a fixed count of agents or lanes, MUST come from the constant
  that the code uses, or the description MUST leave the number out. A
  description of a cap MUST state the set the cap counts and what happens at
  the cap (rejected, oldest entry dropped or ignored). Read the code that
  enforces the cap: a hard-coded number, or a stated outcome that the code
  contradicts, is a finding. This extends the maximum-value bullet above, which
  builds the number from a constant for an input description only. Example: a
  `plan_mark` description that hard-coded a 64-entry limit and a 3-agent count
  and described a cap behavior that the code did not have.
- A list of ids that a check, lane or tool declares as its own (for example the
  `gateIds` of a gate check) MUST hold every id that the check can emit in its
  findings or results, and no id that it never emits. Compare the declared list
  with the ids in the emitting code. A caller that reads the declared list to
  report which checks ran, or which stay unchecked, never names an id that is
  missing from it, so the gap is silent. A test asserts that the declared set
  equals the emitted set. A missing or extra id is a finding. This extends the
  enum-like value bullet above, which covers narration templates only. Example:
  `go-gates` declared `gateIds` without the G18 findings it emits.

## Cross-references

- `mcp-contract-compliance.md` covers the specific structural tag/field
  contracts (`jsonschema_description`, `enum`, `Next`, `Suggestion`)
  introduced by the MCP Tooling Hardening plan — more granular than the
  general quality rules above.
- `skill-wiring-consistency.md` covers SKILL.md-to-tool parameter name
  alignment, a distinct concern from tool-internal contract quality.
