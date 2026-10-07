# tool-setup-write-sections Specification

## Purpose
`setup_write_sections` writes caller-assembled field values into one or more sections of `.sdlc-v2/config.toml` or `.sdlc-v2/local.toml`. The `setup` skill and its sub-flows call it after collecting answers. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input field
The tool SHALL accept two input fields: `sectionsJson`, holding a JSON object whose keys are section keys and whose values are JSON objects (or `null`), and the optional `target`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `sectionsJson` | string | yes | JSON object, e.g. `{"version":{"mode":"file","versionFile":"package.json"}}` | Section key → complete field-value object. A key may be dotted, e.g. `plan.guardrails`. |
| `target` | string (enum `project`, `user`) | no | plain text, e.g. `user`; default `project` | Where local sections go: `project` (`.sdlc-v2/local.toml`) or `user` (the user file, see "Save target for local sections"). |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `sectionsJson` is `""` | `DomainError` | `setup_write_sections: sectionsJson is required` / pass a JSON object of section id to field-value object |
| Not valid JSON, or a value is not an object or `null` | `DomainError` | `setup_write_sections: invalid sectionsJson: <parse error>` / fix the JSON syntax |
| Object has no keys | `DomainError` | `setup_write_sections: sectionsJson must contain at least one section` / pick a section id from `setup_prepare` |
| `target` is not empty, `project` or `user` | `DomainError` | Suggestion `Set target to project or user, or leave it empty.`; nothing is written |

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

### Requirement: File routing
The tool SHALL route each section key by its first dotted segment and SHALL reject the whole call, before writing anything, when any key has a non-empty first segment outside both sets below.

| First segment | Target file |
|---|---|
| `version`, `jira`, `commit`, `pr`, `plan`, `execute` | `.sdlc-v2/config.toml` |
| `review`, `style`, `planStyle`, `ship`, `receivedReview`, `github`, `executePrefs`, `workspace`, `automation` | `.sdlc-v2/local.toml`; the user file when `target` is `user` |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| One or more keys have an unknown first segment | `DomainError` | `setup_write_sections: unknown section keys [<keys>]; allowed top-level keys: [<sorted allowed keys>]` / fix each key to an allowed first segment (the section's `configPath` from `setup_prepare`); nothing was written |

- The local set is the top-level properties of `plugins/sdlc/schemas/sdlc-local.schema.json`, minus its integer `version` property.
- An empty first segment (e.g. `.guardrails`) is not an unknown key; it fails per section as described under "Wholesale write at the named key".
- The tool creates `.sdlc-v2/` and the target file when absent.
- Writes are atomic per file.

#### Scenario: Dotted project key
- **WHEN** `sectionsJson` is `{"pr.labels":{"mapping":{"fix":"bug"}}}`
- **THEN** `.sdlc-v2/config.toml` gets `pr.labels`
- **AND** `.sdlc-v2/local.toml` is not written

#### Scenario: Local key
- **WHEN** `sectionsJson` is `{"ship":{"draft":true}}`
- **THEN** `.sdlc-v2/local.toml` gets the `ship` table

#### Scenario: Misspelled key rejected
- **WHEN** `sectionsJson` is `{"commit":{"style":"conventional"},"shp":{"draft":true}}`
- **THEN** the tool returns a `DomainError` starting `setup_write_sections: unknown section keys [shp]`
- **AND** the message lists the allowed top-level keys
- **AND** neither `.sdlc-v2/config.toml` nor `.sdlc-v2/local.toml` is written

### Requirement: Wholesale write at the named key
The tool SHALL replace the table at the named key wholesale and SHALL leave every other key in the file unchanged. For a dotted key it SHALL replace only the leaf table, keep siblings under the same parent, and create missing parent tables.

- A `null` value writes an empty table at that key, which clears it.
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
- **WHEN** the call writes `{"ship":{"steps":["execute","commit"],"bump":"patch"}}` to a `local.toml` that already holds those values with no comment line above `steps` or `bump`
- **THEN** the key and value lines of `steps` and `bump` are byte-identical to their old text
- **AND** the template tips for `steps` and `bump` are inserted directly above those keys

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
- **WHEN** `local.toml` has no `review` table and holds the template lines `# [review]`, `# scope = "working"` and `# maxDimensions = 8`
- **AND** the call writes `{"review":{"scope":"diff"}}`
- **THEN** `# [review]` becomes `[review]`
- **AND** `# scope = "working"` becomes a live `scope` line with the value `diff`
- **AND** `# maxDimensions = 8` stays a comment line

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

### Requirement: Dotted field names expanded
The tool SHALL expand dotted field names inside a section value into nested tables before writing. Keys that share a prefix SHALL merge into one nested table.

#### Scenario: Version tag fields
- **WHEN** `sectionsJson` is `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`
- **THEN** `config.toml` holds `version.tag.enabled = true`
- **AND** `config.toml` holds `version.tag.prefix = "v"`

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

### Requirement: Per-section failures do not stop the batch
The tool SHALL process section keys in sorted order and SHALL continue after a section fails. A failed section SHALL add an `errors` entry and set `ok: false`; it SHALL NOT produce a tool error.

- Before writing `config.toml`, the tool checks that every top-level key in the merged file is one of `version`, `jira`, `commit`, `pr`, `plan`, `execute`.
- Failure cause: `config: unknown top-level keys in .sdlc-v2/config.toml: [<keys>]`.
- A TOML parse error in the existing target file is also a per-section failure.

#### Scenario: Existing file has an unknown key
- **WHEN** `.sdlc-v2/config.toml` already has a top-level key `foo`
- **AND** the call writes `{"version":{"mode":"file"}}`
- **THEN** `ok` is `false`
- **AND** `errors` has an entry starting `section version: config: unknown top-level keys`
- **AND** `config.toml` is not changed

### Requirement: CI scaffold after version write
When `version` is in `written`, the tool SHALL run the `scaffold_ci` logic once with force off, under the same root as `.sdlc-v2/config.toml`, and SHALL report its file reports in `scaffold`. Scaffold failure SHALL NOT fail the call.

- `scaffold[]` entries: `path`, `action`, `installedVersion`, `currentVersion`, `group`. See the `tool-scaffold-ci` spec for the file list.
- Scaffold warnings are appended to `warnings`.
- A scaffold error becomes a `warnings` entry `scaffold_ci: <cause>`; `ok` is unaffected.
- No scaffold runs when `version` is not written.

#### Scenario: First version write
- **WHEN** `sectionsJson` is `{"version":{"tag.enabled":true,"tag.prefix":"v"}}` in an empty directory
- **THEN** `scaffold` has 8 entries, each with `action: "created"`
- **AND** `.github/workflows/release-on-main.yml` exists

#### Scenario: Repeat version write
- **WHEN** the same call runs a second time
- **THEN** `ok` is `true`
- **AND** every `scaffold` entry has `action: "skipped"`

#### Scenario: Non-version write
- **WHEN** `sectionsJson` is `{"commit":{"style":"conventional"}}`
- **THEN** `scaffold` is omitted
- **AND** `.github/workflows/release-on-main.yml` is not created

### Requirement: Project root
The tool SHALL write `.sdlc-v2/config.toml` and the auto-scaffolded CI files under the active worktree root, SHALL write `.sdlc-v2/local.toml` under the main worktree root, SHALL write the user file at its own path when `target` is `user`, and SHALL use the current working directory for a root that cannot be resolved.

| File | Root | Why |
|---|---|---|
| `.sdlc-v2/config.toml` | active worktree (`git rev-parse --show-toplevel`) | git-tracked; the change belongs to the checked-out branch |
| `.github/scripts/*`, `.github/workflows/*` | active worktree | git-tracked |
| `.sdlc-v2/local.toml` | main worktree | gitignored per-user state shared by all worktrees |
| user file (`$SDLC_USER_CONFIG`, else `~/.sdlc/local.toml`) | none; outside every repository | personal settings shared by all projects |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| A root and the current directory both unresolvable | `InfraError` | `resolve project root: <cause>` / restart the sdlc MCP server from an existing directory, then retry `setup_write_sections` |

- Annotations: `Title: "Write SDLC config sections"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: false`, `OpenWorld: false`.

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs from a linked git worktree with `sectionsJson` `{"version":{"tag.enabled":true},"review":{"scope":"diff"}}`
- **THEN** `.sdlc-v2/config.toml` and `.github/workflows/release-on-main.yml` are written under the linked worktree
- **AND** `.sdlc-v2/local.toml` is written under the main worktree
- **AND** `root` is the linked worktree path

### Requirement: Save target for local sections
With `target: "user"`, the tool SHALL write every local section to the user file (`$SDLC_USER_CONFIG`, else `~/.sdlc/local.toml`) and SHALL NOT write `.sdlc-v2/local.toml`.

- Empty `target` or `project` keeps the project routing.
- The user file gets the same comment-safe write and tip restore. The tool creates it and its directory when absent.
- Each `next`, `errors` and `warnings` text that names a file names the user file path.

#### Scenario: Tool description names the user target
- **WHEN** a client reads the `setup_write_sections` tool description
- **THEN** it states that `target` `user` writes the user file outside every repository
- **AND** it states that a project section with `target` `user` is rejected and nothing is written

#### Scenario: Local section to the user file
- **WHEN** `SDLC_USER_CONFIG` is `/tmp/u/local.toml`
- **AND** the call passes `target: "user"` and `sectionsJson` `{"style":{"audience":"technical"}}`
- **THEN** `/tmp/u/local.toml` has `[style]` with `audience = "technical"`
- **AND** `.sdlc-v2/local.toml` is not written

#### Scenario: Two calls to the user file
- **WHEN** one call writes `style` with `target: "user"`
- **AND** a second call writes `review` with `target: "user"`
- **THEN** the user file has both `[style]` and `[review]`

#### Scenario: Refused section names the user file
- **WHEN** the call passes `target: "user"`
- **AND** the user file has a comment line and the section cannot be edited in place
- **THEN** `next` starts `Edit section <key> in <user file path> by hand`

### Requirement: User target rejections
With `target: "user"`, the tool SHALL return a `DomainError` with a `Suggestion` and write nothing when any key is a project section (`version`, `jira`, `commit`, `pr`, `plan`, `execute`), or when `SDLC_USER_CONFIG` is unset and no home directory is available.

- No home directory: Suggestion `Set SDLC_USER_CONFIG to a file path, or use target project.`

#### Scenario: Project section with the user target
- **WHEN** the call passes `target: "user"` and `sectionsJson` `{"commit":{"style":"conventional"}}`
- **THEN** the tool returns a `DomainError` with a `Suggestion`
- **AND** neither `.sdlc-v2/config.toml` nor the user file is written

#### Scenario: No home directory
- **WHEN** `SDLC_USER_CONFIG` is unset and no home directory is available
- **AND** the call passes `target: "user"` with a local section
- **THEN** the tool returns a `DomainError` with Suggestion `Set SDLC_USER_CONFIG to a file path, or use target project.`
- **AND** nothing is written
