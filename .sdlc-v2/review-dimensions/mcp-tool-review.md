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
- Two helpers that resolve or load the same resource for different actions
  (for example one lookup for a read action and one for a write action) MUST
  return one error shape: the same error class, a `Msg` built the same way and
  a `Suggestion` with the same recovery steps for each state that both can
  reach. The one exception is a difference that the doc comment of a helper
  names, with the reason. The two copies drift on a missing record, a damaged
  record and a record in the wrong state, so a difference with no stated
  reason is a finding. This extends the shared-input-policy bullet above,
  which compares two actions and not the helpers they call. Example: a reuse
  lookup and a mark lookup that returned different errors for the same missing
  run.
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
- A list input and a free-text input that the handler stores or echoes MUST
  have a maximum, as a count or size input does: a maximum entry count for a
  list and a maximum length for text, each held in one named constant. The
  handler rejects a larger input with a `DomainError` that states the limit and
  the value, and the description of the input builds its number from that
  constant. An input with no maximum is accepted at any size and is a finding.
  The value that the handler checks, the value that it stores and the value
  that it echoes are one value: a value trimmed for the check is stored
  trimmed. A handler that cuts, trims or drops part of a value to fit a cap or
  a form MUST say so in the output: a warning names the field, the limit and
  what changed, and a cut with no warning is a finding. This extends the
  maximum-value bullet above, which names count and size inputs only. Example:
  a healing record whose `applied[]` list and `rationale` text had no maximum,
  a `deferredId` stored untrimmed, and a patch cut with no warning.
- Code that cuts text to a byte or rune cap (the output of a validator, a
  stored note, a message) takes the cap from one named constant and cuts at a
  rune boundary: a cut never falls inside a multi-byte character, and valid
  UTF-8 stays valid. The cut is reported: a warning names the field, the limit
  and the part dropped. A cut with a typed-in cap, a cut that splits a
  character, or a cut with no warning is a finding. This extends the
  cut-with-no-warning bullet above, which names stored inputs and not output
  text, and does not name the rune boundary. Example: a validator output cut at
  a literal byte count that split a multi-byte character and added no warning.
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
- An `*Out` field that describes a success result (a URL, a path, a count, a marker) MUST be empty on each path that returns an error or a failure status. A handler that fills the field and then fails shows the caller a value for work that did not happen. Trace each error return of the handler and list the `*Out` fields set before it. A field set on an error path is a finding, unless its `jsonschema_description` says that it holds a value for a failed call and why. A test calls the registered tool, forces the error and asserts the rendered output, including the empty form of the field. This extends the write-action echo bullet above, which requires the echo of a written value, not the absence of a value when the write did not happen. Example: a preview URL field that the handler set after the call that failed.
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
- A file or directory path that a text names MUST come from the constant or
  helper that the code uses to resolve that path (for example `paths.DataDir`
  plus a file-name constant, or a label constant that names the local config
  files), not be typed again as a literal. The texts are the registered tool and
  action description, an error `Msg` or `Suggestion`, a warning, a `Next` text
  and a `jsonschema_description`. Build a runtime text from the constant. A
  struct tag must be a string literal, so a path in a tag needs a test that
  reads the tag and compares the path with the constant, or the tag names the
  file and leaves out the directory. A literal path in a text with neither is a
  finding: the text drifts when the path moves, and one path spelled in two
  texts drifts apart. A text that names one location when the code reads more
  than one (a project file and a user file) is a finding too. This extends the
  descriptor bullet above that checks a named path against the loader. That
  bullet checks that the path is true now, not that it stays true. Example: a
  field description that named the local config file and the user config file
  as typed paths.
- An action that replaces, clears or deletes stored text (a section, a file, a
  row) MUST NOT read an omitted, empty or blank input as 'clear it'. Read what
  the handler does when the input that supplies the new text is missing. An
  omitted input either fails with a `DomainError` that names the input, or
  keeps the stored value and says so in the output. A call that clears text on
  purpose takes an explicit input for that choice, and the registered
  description names each case in which the call discards or replaces text. A
  handler that erases stored text on an omitted input is a finding. A test
  omits the input on a seeded record and asserts the output and that the stored
  text is unchanged. This extends the fail-closed guard bullet above, which
  covers a value of an unexpected shape, not a missing value. Example: a
  section write that cleared the section when `content` was omitted.
- A rejection of an input that has a closed set of values (a section name, an
  action name) or a format rule (a heading shape, a table layout) MUST give the
  caller what it needs to retry. For a closed set, the `Suggestion` lists every
  allowed value, built from the constant that the check uses. For a format
  rule, the `Msg` names the value found and the rule it breaks, and the
  `Suggestion` states the rule with one valid example. The `jsonschema_description`
  of the input or the action description states the same rule, so the caller
  can learn it before the first call. A generic text such as 'check the format'
  is a finding. This extends the actionable-errors bullet above and the warning
  bullet, which asks for the expected format with one example for warnings
  only. Example: an unknown-section error that did not list the valid section
  names.
- A rule that ties one input to some actions of a multi-action tool (a field
  that is valid for one action only, a field that needs another field) MUST
  refuse the other combinations with an error that names the input, the action
  the caller sent, and the actions that accept the input. The `Suggestion` lists
  those actions, built from the constant or table that the check uses, and the
  `jsonschema_description` of the input states the same rule. An error that
  names only the accepted action, or that does not say which action the caller
  sent, is a finding. A test sends the input with each other action and asserts
  the whole `Msg` and `Suggestion`. This extends the closed-set rejection bullet
  above, which covers an unknown value and not a known value sent with the wrong
  action. Example: a `tab` input refused for every action except `ensure`, with
  a text that did not name the action of the caller.
- A struct tag must be a string literal, so a number in a `jsonschema_description`
  tag (a limit, a cap, a fixed count) cannot be built from the constant that
  holds it. Such a tag leaves the number out, or a test reads the tag and
  compares the number with the constant, as the path bullet above requires for a
  path in a tag. A tag that hard-codes a number with neither is a finding: the
  tag goes stale when the limit changes. This extends the stated-number bullet
  above, which asks that a description build its number from the constant and
  which a tag cannot meet. Example: the tag of a ready-list output field that
  typed its count limits as numbers.
- A verdict that a handler or check derives from a list it parsed out of
  external or stored text (clean, approved, none found, a count of N) MUST
  be compared with every count that the text declares for itself, such as an
  overview that states how many comments it holds. A mismatch between the
  declared count and the parsed set, or a declared count with an empty
  parsed set, MUST report 'not verified' with a warning that names both
  numbers, and never reports clean or approved. A test drives a declared
  count above, equal to and absent from the parsed count, and asserts the
  verdict and the warning. This extends the zero-count bullet above, which
  covers an empty item set and a skipped entry but not a count that the
  source declares. Example: an approved-clean verdict that ignored the item
  counts declared in a review overview.
- A refusal of an input that selects a record (a run ID, a thread ID, a path) because the record is missing, or because it belongs to another scope (another thread, session, branch or owner), MUST name where the caller gets a valid value. The text names the tool, action or field that lists or returns the right one, and the value that the stored record holds when the handler knows it. A `Msg` that says the value does not match, with a `Suggestion` that names no source of the right value, is a finding. The named source MUST be reachable with the input that the caller already has. A test sends a value of another scope through the registered tool and asserts the whole `Msg` and `Suggestion`. This extends the closed-set rejection bullet above, which lists the allowed values of a closed set and does not cover an open value. Example: an error for a run ID of another thread that named no way to find the right run ID.
- Output fields that state one fact about a record (a status, a final or finished flag, a completion time, a summary or label text, a count) MUST derive from one value, or the description MUST state which source wins. List each such field and what sets it. Then walk each state of the record (not started, running, done, failed, stopped, time missing) and write the value of every field for that state. A state in which two fields disagree is a finding, for example a done status with a null completion time, or a final label on an open run. A test for each state calls the registered tool and asserts the whole rendered output. This extends the terminal-write precedence bullet above, which covers two writes of one reason and not two fields of one output. Example: a card whose completion time was null while its status and its final text said that the run had ended.
- A new or changed input or output on an action that already exists is a change under the description bullet above, which names a new action and its new output fields. List each input that the action now reads and each field that it now returns. The registered tool description and the line of that action name each one, with the effect of an input when it is set and when it is left out, in the `Requires: ... Optional: ...` form. Check every action of the tool that takes the input or returns the field, not only the action that the diff names. A description that names the old inputs and outputs and omits a new one is a finding. This extends the description bullet above from a new action to a new input or field on an existing action. Example: the registered descriptions of the `read` and `init` actions of `execute_state` omitted `runId` and `t3ThreadId`.
- A value that a handler reads from the process environment or another ambient source (an environment variable such as a thread ID, an inherited setting) MUST NOT be dropped in silence when it is set and malformed. List what the code does for an unset value, an empty value and a malformed value. An unset value takes the documented default. A malformed value either fails with a `DomainError` that names the variable and the rule, or adds a warning that names the variable, the expected format with one example, and the value used instead. A parse that returns `ok=false` and falls through to the default with no warning is a finding. A test sets the variable to a malformed value, to an empty value and to a valid value, calls the registered tool, and asserts the whole output. This extends the zero-count bullet above, which covers a loader that defaults a stored value, and the warning bullet above, which asks for the content of a warning and not for its presence. Example: a malformed `T3CODE_THREAD_ID` that was ignored with no warning.
- Two input fields that a caller can set together MUST have a stated rule for the pair. When one field has no effect, or a different effect, while the other is set (for example a `final` flag beside a `snapshot` flag, or a field that works only with a mode field), the handler refuses the pair with a `DomainError` that names both fields and the rule, or ignores one field and adds a warning that names the ignored field and the field that won. A field that the handler accepts and drops with no signal is a finding. The `jsonschema_description` of each field and the action description state the same rule, and a test through the registered tool drives each pair and each field set alone. This extends the input-validation bullet above, which checks each new field alone. Example: a call with `final` and `snapshot` both true that dropped `snapshot` in silence.

## Cross-references

- `mcp-contract-compliance.md` covers the specific structural tag/field
  contracts (`jsonschema_description`, `enum`, `Next`, `Suggestion`)
  introduced by the MCP Tooling Hardening plan — more granular than the
  general quality rules above.
- `skill-wiring-consistency.md` covers SKILL.md-to-tool parameter name
  alignment, a distinct concern from tool-internal contract quality.
