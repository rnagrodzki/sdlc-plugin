# tool-setup-write-sections Specification

## Purpose
`setup_write_sections` writes caller-assembled field values into one or more sections of `.sdlc-v2/config.toml` or `.sdlc-v2/local.toml`. The `setup` skill and its sub-flows call it after collecting answers. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input field
The tool SHALL accept one input field, `sectionsJson`, holding a JSON object whose keys are section keys and whose values are JSON objects (or `null`).

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `sectionsJson` | string | yes | JSON object, e.g. `{"version":{"mode":"file","versionFile":"package.json"}}` | Section key → complete field-value object. A key may be dotted, e.g. `plan.guardrails`. |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `sectionsJson` is `""` | `DomainError` | `setup_write_sections: sectionsJson is required` / pass a JSON object of section id to field-value object |
| Not valid JSON, or a value is not an object or `null` | `DomainError` | `setup_write_sections: invalid sectionsJson: <parse error>` / fix the JSON syntax |
| Object has no keys | `DomainError` | `setup_write_sections: sectionsJson must contain at least one section` / pick a section id from `setup_prepare` |

#### Scenario: Scalar section value
- **WHEN** `sectionsJson` is `{"version":"x"}`
- **THEN** the tool returns a `DomainError` starting `setup_write_sections: invalid sectionsJson:`
- **AND** writes nothing

#### Scenario: Empty object
- **WHEN** `sectionsJson` is `{}`
- **THEN** the tool returns a `DomainError` `setup_write_sections: sectionsJson must contain at least one section`

### Requirement: Output fields
The tool SHALL return the fields below.

| Field | Meaning |
|---|---|
| `ok` | `true` when every section was written. |
| `written` | Section keys written, in sorted order. |
| `errors` | One `section <key>: <cause>` entry per failed section; omitted when none. |
| `scaffold` | CI file reports from the `version` auto-scaffold; omitted when it did not run. |
| `warnings` | Full-rewrite fallback warnings (see "File text kept outside the written section") and scaffold warnings; omitted when none. |

#### Scenario: Single section written
- **WHEN** `sectionsJson` is `{"commit":{"style":"conventional"}}`
- **THEN** `ok` is `true`
- **AND** `written` is `["commit"]`

### Requirement: File routing
The tool SHALL route each section key by its first dotted segment and SHALL reject the whole call, before writing anything, when any key has a non-empty first segment outside both sets below.

| First segment | Target file |
|---|---|
| `version`, `jira`, `commit`, `pr`, `plan`, `execute` | `.sdlc-v2/config.toml` |
| `review`, `planStyle`, `ship`, `receivedReview`, `github`, `executePrefs`, `workspace`, `automation` | `.sdlc-v2/local.toml` |

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
The tool SHALL change only the text of the written section in the target file and SHALL keep every other byte, including comments, blank lines and other sections, unchanged.

- The section's text is every table header at or below the key (`[x]`, `[x.y]`, `[[x.y]]`), from the header line through its last key/value line, plus any key/value line outside those tables whose full key is at or below the key (e.g. `y.z = 1` under `[x]` when the key is `x.y`).
- The new section text is encoded with go-toml. It goes where the first of those tables was. The other tables are deleted, together with the blank lines right after them.
- When the file has no table for the key, the new text is appended at the end of the file after exactly one blank line.
- Comment rule: comment and blank lines above a table header stay, and comment and blank lines after a table's last key/value stay. Comment lines between a replaced header and its last key/value are lost.
- Headers are found with the go-toml parser, so `[` inside a multi-line string, a multi-line array or a comment is never taken for a header.
- New text uses `\n` line endings, even in a file that uses `\r\n`.
- Safety check: the spliced text must decode to exactly the data a full rewrite of the merged file would decode to. When it does not, or when the key lives inside an inline table, a dotted key that defines a parent, or an array of tables, the tool rewrites the whole file from parsed data (all comments in that file are lost) and adds a `warnings` entry `section <key>: could not edit <file> in place, so the whole file was rewritten and its comments were removed`.
- `config.toml` and `local.toml` use the same writer.

#### Scenario: Template comments survive a write
- **WHEN** `.sdlc-v2/config.toml` holds the shipped commented template
- **AND** the call writes `{"commit":{"allowedTypes":["feat","fix"],"allowedScopes":["api"]}}`
- **THEN** the file equals the template with only the `[commit]` table text replaced
- **AND** `warnings` is omitted

#### Scenario: Absent section appended
- **WHEN** `config.toml` has no `jira` table
- **AND** the call writes `{"jira":{"defaultProject":"PROJ"}}`
- **THEN** `[jira]` is appended at the end of the file after one blank line
- **AND** all earlier text is unchanged

#### Scenario: Sub-tables replaced as one unit
- **WHEN** `config.toml` has `[plan.guardrails.a]` and `[plan.guardrails.b]`, with a `[jira]` table between them
- **AND** the call writes `{"plan.guardrails":{"c":{"severity":"error"}}}`
- **THEN** `[plan.guardrails.c]` takes the place of `[plan.guardrails.a]`
- **AND** `[plan.guardrails.b]` is deleted
- **AND** the `[jira]` table and its comments are unchanged

#### Scenario: Layout that cannot be spliced
- **WHEN** `config.toml` defines `plan = { tasks = { note = "old" } }`
- **AND** the call writes `{"plan.tasks":{"note":"new"}}`
- **THEN** `plan.tasks.note` is `new`
- **AND** `warnings` has an entry starting `section plan.tasks: could not edit .sdlc-v2/config.toml in place`

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

- This applies only to the section being written. On the full-rewrite fallback, other sections are re-encoded from the parsed file, which holds every number as a float, so a whole number there is written as e.g. `60.0`.

#### Scenario: Nested integer fields
- **WHEN** `sectionsJson` is `{"automation":{"reviewFixIterations":3,"drift":{"maxErrorRate":0.5,"minErrorFloor":2}}}`
- **THEN** `local.toml` holds `reviewFixIterations = 3`
- **AND** `local.toml` holds `minErrorFloor = 2`
- **AND** `local.toml` holds `maxErrorRate = 0.5`

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
When `version` is in `written`, the tool SHALL run the `scaffold_ci` logic once with force off and SHALL report its file reports in `scaffold`. Scaffold failure SHALL NOT fail the call.

- `scaffold[]` entries: `path`, `action`, `installedVersion`, `currentVersion`, `group`. See the `tool-scaffold-ci` spec for the file list.
- Scaffold warnings are appended to `warnings`.
- A scaffold error becomes a `warnings` entry `scaffold_ci: <cause>`; `ok` is unaffected.
- No scaffold runs when `version` is not written.

#### Scenario: First version write
- **WHEN** `sectionsJson` is `{"version":{"tag.enabled":true,"tag.prefix":"v"}}` in an empty directory
- **THEN** `scaffold` has 10 entries, each with `action: "created"`
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
The tool SHALL resolve the project root to the main worktree root and fall back to the current working directory when that fails.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root and current directory both unresolvable | `InfraError` | `resolve project root: <cause>` / restart the sdlc MCP server from an existing directory, then retry `setup_write_sections` |

- Annotations: `Title: "Write SDLC config sections"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: false`, `OpenWorld: false`.

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs from a linked git worktree
- **THEN** the config files under the main worktree root are written
