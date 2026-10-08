# Spec Delta

## MODIFIED Requirements

### Requirement: File text kept outside the written section
The tool SHALL change only the lines of changed keys in the written section and SHALL keep every comment line, blank line and other section of the target file unchanged, except for the commented example and header lines that a new key or section takes over and the tip lines that tip restore adds.

- The section's text is every table header at or below the key (`[x]`, `[x.y]`, `[[x.y]]`), from the header line through its last key/value line, plus any key/value line outside those tables whose full key is at or below the key (e.g. `y.z = 1` under `[x]` when the key is `x.y`).
- A changed key keeps its key text, indentation and trailing `# comment`. Only its value text changes.
- An unchanged key keeps its bytes, order and quote style. A write with no value change leaves the file byte-identical.
- A removed key loses its key/value lines. A removed sub-table loses its header line, its key/value lines and one blank line after it. Comment lines stay.
- A new key replaces the first commented example line for it (`# <key> = <value>`, any spacing) between its table header and the next header, and keeps that line's trailing `# <tip>` with its spacing. A commented line inside a multi-line value is never taken for an example. With no example line, a new key goes after the last key of its table. A new sub-table goes after the last line of its section. When every old block of the section is removed, new sub-tables go at the first old block.
- A key that is an array of tables, or that changes between a table and an array of tables, is encoded again at its first old block, below its old comment lines.
- When the file has no table for the key and has a commented header line for the key (`# [x]`), that line becomes the live header `[x]`, and each new key of the section replaces its commented example line below that header as described above. Otherwise the new text is appended at the end of the file after exactly one blank line.
- Headers are found with the go-toml parser, so `[` inside a multi-line string, a multi-line array or a comment is never taken for a header.
- Line endings: when the file's first line ends in `\r\n`, the new text and the blank-line separator before an appended section use `\r\n`; otherwise they use `\n`.
- Safety check: the edited text must decode to exactly the data a full rewrite of the merged file would decode to. The edit cannot run when the key lives inside an inline table, a dotted key that defines a parent, or an array of tables that is a strict ancestor of the key.
- When the edit cannot run or fails the safety check and the file has a comment line, the section is not written. `errors` gets `section <key>: <cause>`, and `next` gets `Edit section <key> in <file> by hand, then run setup again for that section. Other sections were written.` When more than one section is refused in one call, `next` names each of them, in sorted order: `Edit section <key1> in <file1> by hand; Edit section <key2> in <file2> by hand, then run setup again for those sections. Other sections were written.`
- When the edit cannot run or fails the safety check and the file is missing or has no comment line, the tool rewrites the whole file from parsed data and adds a `warnings` entry `Section <key>: could not edit <file> in place; the file had no comments, so it was rewritten and template tips were added`.
- Tip restore: after each write of `config.toml`, `local.toml` or the user file, each key and header in the whole file gets the shipped template's tip inserted above it when the file has no comment line directly above it. A key or header that already has a comment line directly above it is not changed. The file that was not written gets no tip.
- Template tips: a live template key's tip is the comment block above it. A key that the template shows only as a commented example (`# <key> = <value>   # <tip>`) has as its tip the comment block above that line, then its trailing `# <tip>` as one `#` line; when both are empty, the key has no tip. A commented header (`# [x]`) in the template sets the section of the example lines below it. A comment block stops at a commented example or commented header line. When a live key and a commented example have the same path, the live key's tip is used.
- The tip restore adds only comment lines. When the restore fails, or its output decodes to other data, the tool writes the text without the restore and reports no error.
- `config.toml` and `local.toml` use the same writer.

#### Scenario: Template comments survive a write
- **WHEN** `.sdlc-v2/config.toml` holds the shipped commented template
- **AND** the call writes `{"commit":{"allowedTypes":["feat","fix"],"allowedScopes":["api"]}}`
- **THEN** only the value text of `allowedTypes` and `allowedScopes` changes
- **AND** every comment line of the template is still in the file
- **AND** `warnings` is omitted

#### Scenario: No value change
- **WHEN** the call writes the values that the file already holds
- **AND** each key of the file that the template documents already has a comment line directly above it
- **THEN** the file is byte-identical to its old content

#### Scenario: No value change, tip missing
- **WHEN** the call writes `{"ship":{"bump":"patch","draft":false}}` to a `local.toml` that already holds those values with no comment line above `bump` or `draft`
- **THEN** the key and value lines of `bump` and `draft` are byte-identical to their old text
- **AND** the template tips for `bump` and `draft` are inserted directly above those keys

#### Scenario: Absent section appended
- **WHEN** `config.toml` has no `jira` table and no `# [jira]` comment line
- **AND** the call writes `{"jira":{"defaultProject":"PROJ"}}`
- **THEN** `[jira]` is appended at the end of the file after one blank line
- **AND** the line above `defaultProject` is its template tip
- **AND** all earlier text is unchanged

#### Scenario: Absent section placed after its commented header
- **WHEN** `config.toml` has no `jira` table and has a comment block with the line `# [jira]`
- **AND** the call writes `{"jira":{"defaultProject":"PROJ"}}`
- **THEN** the line `# [jira]` becomes `[jira]`
- **AND** `jira.defaultProject` is `PROJ`
- **AND** every other comment line of that block stays

#### Scenario: Commented header and example uncommented
- **WHEN** `local.toml` has no `review` table and holds the template lines `# [review]`, `# scope = "working"` and `# maxParallelDimensions = 8`
- **AND** the call writes `{"review":{"scope":"diff"}}`
- **THEN** `# [review]` becomes `[review]`
- **AND** `# scope = "working"` becomes a live `scope` line with the value `diff`
- **AND** `# maxParallelDimensions = 8` stays a comment line

#### Scenario: Commented example replaced, trailing tip kept
- **WHEN** the `[style]` table of `local.toml` holds `# audience = "functional"      # technical | functional | executive | general | beginner` and no live `audience`
- **AND** the call writes `{"style":{"audience":"technical"}}`
- **THEN** that line becomes `audience = 'technical'      # technical | functional | executive | general | beginner`
- **AND** no other `audience` line is added

#### Scenario: Two examples for one key
- **WHEN** the `[style]` table has two commented example lines for `tone`
- **AND** the call writes `tone`
- **THEN** the first example line becomes the live key
- **AND** the second example line stays a comment line

#### Scenario: Comment inside a multi-line value
- **WHEN** a multi-line array in the `[ship]` table holds the line `# auto = false`
- **AND** the call writes `ship` with a new key `auto`
- **THEN** that line inside the array is not changed

#### Scenario: Sub-tables replaced as one unit
- **WHEN** `config.toml` has `[plan.guardrails.a]` and `[plan.guardrails.b]`, with a `[jira]` table between them
- **AND** the call writes `{"plan.guardrails":{"c":{"severity":"error"}}}`
- **THEN** `[plan.guardrails.c]` goes where `[plan.guardrails.a]` was
- **AND** the header and key/value lines of `[plan.guardrails.a]` and `[plan.guardrails.b]` are deleted
- **AND** the comment lines above both old tables stay
- **AND** the `[jira]` table and its comments are unchanged

#### Scenario: CRLF file keeps CRLF
- **WHEN** `config.toml` uses `\r\n` line endings and has a `[commit]` table and no `[jira]` table
- **AND** the call writes `{"commit":{"allowedTypes":["fix"]},"jira":{"defaultProject":"PROJ"}}`
- **THEN** every line of the file, including the new `[commit]` and `[jira]` text and the restored tips, ends in `\r\n`
- **AND** `warnings` is omitted

#### Scenario: Layout that cannot be spliced
- **WHEN** `config.toml` defines `plan = { tasks = { note = "old" } }`
- **AND** the call writes `{"plan.tasks":{"note":"new"}}`
- **THEN** `plan.tasks.note` is `new`
- **AND** `warnings` has an entry starting `Section plan.tasks: could not edit .sdlc-v2/config.toml in place`

#### Scenario: Layout that cannot be spliced, file with comments
- **WHEN** `config.toml` has a comment line and defines `plan = { tasks = { note = "old" } }`
- **AND** the call writes `{"plan.tasks":{"note":"new"}}`
- **THEN** the file is unchanged
- **AND** `ok` is `false`
- **AND** `errors` has an entry starting `section plan.tasks:`
- **AND** `next` starts `Edit section plan.tasks in .sdlc-v2/config.toml by hand`

#### Scenario: Layout that cannot be spliced, file without comments
- **WHEN** `config.toml` has no comment line and defines `plan = { tasks = { note = "old" } }`
- **AND** the call writes `{"plan.tasks":{"note":"new"}}`
- **THEN** `plan.tasks.note` is `new`
- **AND** `warnings` has an entry starting `Section plan.tasks: could not edit .sdlc-v2/config.toml in place; the file had no comments`

#### Scenario: Lost tips restored
- **WHEN** `local.toml` has a `[ship]` table with no comment lines
- **AND** the call writes `ship`
- **THEN** each `[ship]` key that the template documents has its template tip directly above it
- **AND** only comment lines are added outside the `ship` keys that changed

#### Scenario: Tips restored in another section
- **WHEN** `local.toml` has a `[style]` table with a live `audience` key and no comment line above it
- **AND** the call writes `ship`
- **THEN** the line `# technical | functional | executive | general | beginner` is inserted directly above `audience`

#### Scenario: Key with a comment above is not changed
- **WHEN** a key in `local.toml` has a user comment line directly above it
- **AND** the call writes any section of `local.toml`
- **THEN** no tip is inserted above that key
- **AND** the user comment line is unchanged

## ADDED Requirements

### Requirement: Flag-set answers stored as tables
Before it writes any section, the tool SHALL convert each `flag-set` field answer (`ship.steps`, `ship.quick`) from an array of selected step names to a table of every step name → `true` or `false`, and SHALL write nothing when an answer is not valid.

- A table answer passes through with no added keys. A JSON `null` answer clears the key, as today.
- An error is a `DomainError` with a `Suggestion`. The scenarios below give each error text.

#### Scenario: Array answer becomes a table
- **WHEN** `local.toml` has `[ship]` with `steps = ["execute", "review", "harden"]` and `bump = "patch"`
- **AND** the call writes `{"ship":{"steps":["execute","review","harden"],"bump":"patch"}}`
- **THEN** the file has a `[ship.steps]` table with all ten step names
- **AND** `execute`, `review` and `harden` are `true` and the other seven are `false`
- **AND** `ship.bump` is still `patch` and the old `steps = [` line is gone

#### Scenario: Quick answer becomes a table
- **WHEN** `local.toml` has `quick = ["execute"]` under `[ship]`
- **AND** the call writes the `ship` section with `"quick": ["execute","pr"]`
- **THEN** the file has a `[ship.quick]` table with ten keys
- **AND** only `execute` and `pr` are `true`

#### Scenario: Empty answer
- **WHEN** the call writes `{"ship":{"steps":[]}}`
- **THEN** the `[ship.steps]` table has ten keys and every value is `false`

#### Scenario: Table answer passes through
- **WHEN** the call writes `{"ship":{"steps":{"execute":true,"harden":true}}}`
- **THEN** the step table holds only the keys `execute` and `harden`

#### Scenario: Old multi-line list replaced
- **WHEN** `local.toml` has `steps = [` followed by `"execute",` and `]` on separate lines under `[ship]`
- **AND** the call writes a `steps` array answer
- **THEN** the old list is gone and the step table is written

#### Scenario: Old inline table replaced
- **WHEN** `local.toml` has `steps = { execute = true }` under `[ship]`
- **AND** the call writes a `steps` array answer
- **THEN** the old inline table is gone and the step table is written

#### Scenario: Existing header table replaced
- **WHEN** `local.toml` already has a `[ship.steps]` header table
- **AND** the call writes a `steps` array answer
- **THEN** the file has exactly one `[ship.steps]` table, with the new values

#### Scenario: Unknown step name
- **WHEN** the call writes `{"ship":{"steps":["hardn"]}}`
- **THEN** the tool returns a `DomainError` with the message `ship.steps answer has an unknown step "hardn".`
- **AND** the `Suggestion` is `Use only these names: execute, commit, review, verify-openspec, archive-openspec, harden, pr, verify-pipeline, await-remote-review, learnings-commit.`
- **AND** no section of the call is written

#### Scenario: Unknown key in a table answer
- **WHEN** the call writes `{"ship":{"steps":{"hardn":true}}}`
- **THEN** the tool returns a `DomainError` that names `hardn`
- **AND** the file is unchanged

#### Scenario: Non-bool table value
- **WHEN** the call writes `{"ship":{"steps":{"harden":"yes"}}}`
- **THEN** the tool returns a `DomainError` with the message `ship.steps.harden must be true or false, got "yes".`
- **AND** the `Suggestion` is `Send true or false for each step, or send an array of step names.`
- **AND** the file is unchanged

#### Scenario: Scalar answer
- **WHEN** the call writes `{"ship":{"steps":"harden"}}`
- **THEN** the tool returns a `DomainError` with the message `ship.steps answer must be an array of step names or a table of step → true/false, got string.`
- **AND** the `Suggestion` is `Send an array of step names, for example ["execute","review"].`
- **AND** the file is unchanged

#### Scenario: Same answer twice
- **WHEN** the call writes the same `steps` array answer two times
- **THEN** the file bytes after the second write equal the file bytes after the first write

#### Scenario: Old list replaced by a table
- **WHEN** `local.toml` has `steps = ["execute"]` under `[ship]`
- **AND** the call writes a `steps` array answer
- **THEN** no `steps = [` line remains in the file
