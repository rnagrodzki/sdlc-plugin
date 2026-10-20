# Spec Delta

## MODIFIED Requirements

### Requirement: Input field
The tool SHALL accept two input fields: `sectionsJson`, holding a JSON object whose keys are section keys and whose values are JSON objects (or `null`), and the optional `target`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `sectionsJson` | string | yes | JSON object, e.g. `{"version":{"mode":"file","versionFile":"package.json"}}` | Section key → complete field-value object. A key may be dotted, e.g. `plan.tasks`. A key that replaces a whole guardrails table must keep every guardrail id of the file (see "Whole guardrails table keeps every id"). |
| `target` | string (enum `project`, `user`) | no | plain text, e.g. `user`; default `project` | Where local sections go: `project` (`.sdlc-v2/local.toml`) or `user` (the user file, see "Save target for local sections"). |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `sectionsJson` is `""` | `DomainError` | `setup_write_sections: sectionsJson is required` / pass a JSON object of section id to field-value object |
| Not valid JSON, or a value is not an object or `null` | `DomainError` | `setup_write_sections: invalid sectionsJson: <parse error>` / fix the JSON syntax |
| Object has no keys | `DomainError` | `setup_write_sections: sectionsJson must contain at least one section` / pick a section id from `setup_prepare` |
| `target` is not empty, `project` or `user` | `DomainError` | Suggestion `Set target to project or user, or leave it empty.`; nothing is written |
| A key `plan`, `plan.guardrails`, `execute` or `execute.guardrails` removes one or more guardrail ids of `config.toml` | `DomainError` | `setup_write_sections: key "<key>" would remove guardrails [<ids>] from <section>.guardrails. Nothing was written.` / write each guardrail as `<section>.guardrails.<id>`; send `null` on one id to remove it; for the array-of-tables form, convert it to named tables by hand |
| The call holds such a key and `config.toml` is not valid TOML | `DataError` | `setup_write_sections: <path> is not valid TOML: <cause>` / fix the TOML syntax, then retry; nothing was written |
| The call holds such a key and `config.toml` is larger than 1048576 bytes | `DataError` | `setup_write_sections: <path> is <n> bytes, over the 1048576 byte limit of the guardrail check` / write one guardrail at a time, or reduce the file; nothing was written |
| The call holds such a key and `config.toml` cannot be statted or read | `InfraError` | `setup_write_sections: read <path>: <cause>` / check that the path is a readable file; nothing was written |

#### Scenario: Scalar section value
- **WHEN** `sectionsJson` is `{"version":"x"}`
- **THEN** the tool returns a `DomainError` starting `setup_write_sections: invalid sectionsJson:`
- **AND** writes nothing

#### Scenario: Empty object
- **WHEN** `sectionsJson` is `{}`
- **THEN** the tool returns a `DomainError` `setup_write_sections: sectionsJson must contain at least one section`

#### Scenario: Invalid target
- **WHEN** `target` is `home`
- **THEN** the tool returns a `DomainError` with Suggestion `Set target to project or user, or leave it empty.`
- **AND** writes nothing

### Requirement: Wholesale write at the named key
The tool SHALL replace the table at the named key wholesale and SHALL leave every other key in the file unchanged, except that a key which replaces a whole guardrails table SHALL keep every guardrail id of the file (see "Whole guardrails table keeps every id"). For a dotted key it SHALL replace only the leaf table, keep siblings under the same parent, and create missing parent tables.

- A `null` value on `plan.guardrails.<id>` or `execute.guardrails.<id>` deletes that table. A missing table changes nothing, and the summary says `<id>: not present, nothing deleted`.
- A guardrail table that holds a comment line is not deleted: the call returns the comment refusal.
- A `null` guardrail id with `target: "user"` returns a DomainError `guardrail ids live in config.toml`.
- A `null` value on any other key writes an empty table at that key, which clears it. The exception is a `null` on `plan`, `plan.guardrails`, `execute` or `execute.guardrails` while `config.toml` holds a guardrail id in that section: the call is refused.
- Callers pass the complete object for the key; the tool does not patch individual fields.
- A key with an empty dotted segment (e.g. `plan.`, `.guardrails`, `plan..tasks`) fails with `config: invalid section name "<key>"` in `errors`.

#### Scenario: Other sections kept
- **WHEN** `config.toml` has a `version` table
- **AND** the call writes `{"jira":{"defaultProject":"TEST"}}`
- **THEN** `version` is unchanged
- **AND** `jira.defaultProject` is `TEST`

#### Scenario: Dotted leaf keeps siblings
- **WHEN** `config.toml` has `plan.guardrails`
- **AND** the call writes `{"plan.tasks":{...}}`
- **THEN** `plan.guardrails` is unchanged

#### Scenario: Null clears a leaf
- **WHEN** the call writes `{"plan.tasks":null}`
- **THEN** `plan.tasks` becomes an empty table

#### Scenario: Null deletes a guardrail
- **WHEN** `config.toml` has `[plan.guardrails.dry]` and `[plan.guardrails.dry-old]`
- **AND** the call writes `{"plan.guardrails.dry-old":null}`
- **THEN** the `[plan.guardrails.dry-old]` block is gone
- **AND** the `[plan.guardrails.dry]` block is byte-identical

#### Scenario: Null on a guardrails table with no ids
- **WHEN** `config.toml` has an empty `[plan.guardrails]` table
- **AND** the call writes `{"plan.guardrails":null}`
- **THEN** `plan.guardrails` stays an empty table

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
- **AND** the call writes `{"plan.guardrails":{"a":{"severity":"error"},"b":{"severity":"error"},"c":{"severity":"error"}}}`
- **THEN** `[plan.guardrails.a]`, `[plan.guardrails.b]` and `[plan.guardrails.c]` are written at the place of the old `[plan.guardrails.a]` block
- **AND** the old header and key/value lines of `[plan.guardrails.a]` and `[plan.guardrails.b]` are replaced by the new text
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

### Requirement: Whole guardrails table keeps every id
The tool SHALL refuse a call, before it writes any key, when a key `plan`, `plan.guardrails`, `execute` or `execute.guardrails` would remove a guardrail id that `config.toml` holds in that section.

- A covered key counts with an object value and with `null`.
- A one-id key (`<section>.guardrails.<id>`) is not covered and needs no read.
- The tool reads `config.toml` once, only when the call holds a covered key.

#### Scenario: Whole table drops an id
- **WHEN** `config.toml` has `[execute.guardrails.a]` and `[execute.guardrails.b]`
- **AND** the call writes `{"execute.guardrails":{"a":{"severity":"error","description":"d"}}}`
- **THEN** the tool returns a `DomainError` containing `would remove guardrails [b]`
- **AND** `config.toml` is byte-identical

#### Scenario: Null on a table that holds ids
- **WHEN** `config.toml` has `[plan.guardrails.a]`
- **AND** the call writes `{"plan.guardrails":null}`
- **THEN** the tool returns a `DomainError` containing `would remove guardrails [a]`
- **AND** `config.toml` is byte-identical

#### Scenario: Section key without guardrails
- **WHEN** `config.toml` has `[plan.guardrails.a]`
- **AND** the call writes `{"plan":{"tasks":{}}}`
- **THEN** the tool returns a `DomainError` containing `would remove guardrails [a]`

#### Scenario: Whole table keeps every id
- **WHEN** `config.toml` has `[execute.guardrails.a]` and `[execute.guardrails.b]`
- **AND** the call writes `execute.guardrails` with the ids `a`, `b` and `c`
- **THEN** the tool writes the three tables

#### Scenario: Array-of-tables form
- **WHEN** `config.toml` holds `[[plan.guardrails]]` entries with the ids `a` and `b`, and one more entry with the id `a`
- **AND** the call writes `plan.guardrails` with only the id `a`
- **THEN** the tool returns a `DomainError` containing `would remove guardrails [b]`
- **AND** the id `a` is not listed twice

#### Scenario: One-id key keeps the others
- **WHEN** `config.toml` has `[execute.guardrails.a]` and `[execute.guardrails.b]`
- **AND** the call writes `{"execute.guardrails.a":{"severity":"warning","description":"d"}}`
- **THEN** `[execute.guardrails.b]` is unchanged

#### Scenario: Two refused keys
- **WHEN** `plan.guardrails` and `execute.guardrails` each drop an id
- **THEN** the tool returns one `DomainError` that names both keys in sorted order

#### Scenario: Refused key stops the whole call
- **WHEN** one key of the call is a one-id key
- **AND** another key would remove a guardrail id
- **THEN** the tool returns the `DomainError`
- **AND** no key of the call is written

#### Scenario: Absent config file
- **WHEN** `.sdlc-v2/config.toml` does not exist
- **AND** the call writes `{"execute.guardrails":{"a":{"severity":"error","description":"d"}}}`
- **THEN** the tool writes the file

#### Scenario: Guardrails value is not a table
- **WHEN** `config.toml` holds `plan.guardrails = "x"`
- **AND** the call writes `plan.guardrails` with the id `a`
- **THEN** the tool replaces the value, because a value that is not a table holds no ids

#### Scenario: Invalid TOML
- **WHEN** `config.toml` is not valid TOML
- **AND** the call holds a covered key
- **THEN** the tool returns a `DataError` containing `is not valid TOML`
- **AND** nothing is written
