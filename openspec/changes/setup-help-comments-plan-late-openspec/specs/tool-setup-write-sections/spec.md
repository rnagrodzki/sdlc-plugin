## MODIFIED Requirements

### Requirement: Output fields
The tool SHALL return the fields below.

| Field | Meaning |
|---|---|
| `ok` | `true` when every section was written. |
| `written` | Section keys written, in sorted order. |
| `root` | Absolute path of the worktree that holds the written `.sdlc-v2/config.toml` and any scaffolded CI files. |
| `errors` | One `section <key>: <cause>` entry per failed section; omitted when none. |
| `next` | Recovery step when a section was not written because a rewrite would delete comments (see "File text kept outside the written section"); omitted otherwise. |
| `scaffold` | CI file reports from the `version` auto-scaffold; omitted when it did not run. |
| `warnings` | Full-rewrite warnings for files with no comment line (see "File text kept outside the written section") and scaffold warnings; omitted when none. |

#### Scenario: Single section written
- **WHEN** `sectionsJson` is `{"commit":{"style":"conventional"}}`
- **THEN** `ok` is `true`
- **AND** `written` is `["commit"]`
- **AND** `next` is omitted

### Requirement: File text kept outside the written section
The tool SHALL change only the lines of changed keys in the written section and SHALL keep every comment line, blank line and other section of the target file unchanged.

- The section's text is every table header at or below the key (`[x]`, `[x.y]`, `[[x.y]]`), from the header line through its last key/value line, plus any key/value line outside those tables whose full key is at or below the key (e.g. `y.z = 1` under `[x]` when the key is `x.y`).
- A changed key keeps its key text, indentation and trailing `# comment`. Only its value text changes.
- An unchanged key keeps its bytes, order and quote style. A write with no value change leaves the file byte-identical.
- A removed key loses its key/value lines. A removed sub-table loses its header line, its key/value lines and one blank line after it. Comment lines stay.
- A new key goes after the last key of its table. A new sub-table goes after the last line of its section. When every old block of the section is removed, new sub-tables go at the first old block.
- A key that is an array of tables, or that changes between a table and an array of tables, is encoded again at its first old block, below its old comment lines.
- When the file has no table for the key and has a comment block with a commented header for the key (`# [x]`), the new text goes directly after that comment block. Otherwise it is appended at the end of the file after exactly one blank line.
- Headers are found with the go-toml parser, so `[` inside a multi-line string, a multi-line array or a comment is never taken for a header.
- Line endings: when the file's first line ends in `\r\n`, the new text and the blank-line separator before an appended section use `\r\n`; otherwise they use `\n`.
- Safety check: the edited text must decode to exactly the data a full rewrite of the merged file would decode to. The edit cannot run when the key lives inside an inline table, a dotted key that defines a parent, or an array of tables that is a strict ancestor of the key.
- When the edit cannot run or fails the safety check and the file has a comment line, the section is not written. `errors` gets `section <key>: <cause>`, and `next` gets `Edit section <key> in <file> by hand, then run setup again for that section. Other sections were written.` When more than one section is refused in one call, `next` names each of them, in sorted order: `Edit section <key1> in <file1> by hand; Edit section <key2> in <file2> by hand, then run setup again for those sections. Other sections were written.`
- When the edit cannot run or fails the safety check and the file is missing or has no comment line, the tool rewrites the whole file from parsed data and adds a `warnings` entry `Section <key>: could not edit <file> in place; the file had no comments, so it was rewritten and template tips were added`.
- Tip restore: after each write of `config.toml` or `local.toml`, each comment block of the shipped template for a key or header at or below the written key is inserted above that key or header when the file has no comment line directly above it. Other keys, other sections and other files get no tip.
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
- **THEN** the file is byte-identical to its old content

#### Scenario: Absent section appended
- **WHEN** `config.toml` has no `jira` table and no `# [jira]` comment line
- **AND** the call writes `{"jira":{"defaultProject":"PROJ"}}`
- **THEN** `[jira]` is appended at the end of the file after one blank line
- **AND** the line above `defaultProject` is its template tip
- **AND** all earlier text is unchanged

#### Scenario: Absent section placed after its commented header
- **WHEN** `config.toml` has no `jira` table and has a comment block with the line `# [jira]`
- **AND** the call writes `{"jira":{"defaultProject":"PROJ"}}`
- **THEN** `[jira]` goes directly after that comment block

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
- **AND** no section other than `ship` changes

### Requirement: Whole numbers written as integers
The tool SHALL write every JSON number that has no fraction as a TOML integer, at any depth in the section value, including inside arrays. A number with a fraction SHALL stay a TOML float.

| JSON value | Written TOML |
|---|---|
| `60` | `60` |
| `60.0` | `60` |
| `0.5` | `0.5` |
| `[1, 2.5]` | `[1, 2.5]` |

- On the full-rewrite fallback, the same rule applies to every section of the file, not only the one being written. The parsed file holds every number as a float, so each whole number is converted back to an integer before the file is written.

#### Scenario: Nested integer fields
- **WHEN** `sectionsJson` is `{"automation":{"reviewFixIterations":3,"drift":{"maxErrorRate":0.5,"minErrorFloor":2}}}`
- **THEN** `local.toml` holds `reviewFixIterations = 3`
- **AND** `local.toml` holds `minErrorFloor = 2`
- **AND** `local.toml` holds `maxErrorRate = 0.5`

#### Scenario: Full-rewrite fallback keeps other integers
- **WHEN** `local.toml` defines `workspace = { tasks = { note = "old" } }` and a `[ship]` table with `executeWaveInterval = 60`
- **AND** the call writes `{"workspace.tasks":{"note":"new"}}`
- **THEN** the whole file is rewritten and `warnings` has an entry starting `Section workspace.tasks: could not edit .sdlc-v2/local.toml in place`
- **AND** `local.toml` holds `executeWaveInterval = 60`, not `60.0`
