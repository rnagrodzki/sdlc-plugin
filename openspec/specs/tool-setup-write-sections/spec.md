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
| `warnings` | Scaffold warnings; omitted when none. |

#### Scenario: Single section written
- **WHEN** `sectionsJson` is `{"commit":{"style":"conventional"}}`
- **THEN** `ok` is `true`
- **AND** `written` is `["commit"]`

### Requirement: File routing
The tool SHALL route each section key by its first dotted segment: `version`, `jira`, `commit`, `pr`, `plan`, `execute` go to `.sdlc-v2/config.toml`; every other first segment goes to `.sdlc-v2/local.toml`.

- Unknown first segments are not rejected; they land in `local.toml`.
- The tool creates `.sdlc-v2/` and the target file when absent.
- Writes are atomic per file.

#### Scenario: Dotted project key
- **WHEN** `sectionsJson` is `{"pr.labels":{"mapping":{"fix":"bug"}}}`
- **THEN** `.sdlc-v2/config.toml` gets `pr.labels`
- **AND** `.sdlc-v2/local.toml` is not written

#### Scenario: Local key
- **WHEN** `sectionsJson` is `{"ship":{"draft":true}}`
- **THEN** `.sdlc-v2/local.toml` gets the `ship` table

### Requirement: Wholesale write at the named key
The tool SHALL replace the table at the named key wholesale and SHALL leave every other key in the file unchanged. For a dotted key it SHALL replace only the leaf table, keep siblings under the same parent, and create missing parent tables.

- A `null` value writes an empty table at that key, which clears it.
- Callers pass the complete object for the key; the tool does not patch individual fields.
- A key with an empty dotted segment (e.g. `plan.`, `.guardrails`, `a..b`) fails with `config: invalid section name "<key>"` in `errors`.

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

### Requirement: Dotted field names expanded
The tool SHALL expand dotted field names inside a section value into nested tables before writing. Keys that share a prefix SHALL merge into one nested table.

#### Scenario: Version tag fields
- **WHEN** `sectionsJson` is `{"version":{"tag.enabled":true,"tag.prefix":"v"}}`
- **THEN** `config.toml` holds `version.tag.enabled = true`
- **AND** `config.toml` holds `version.tag.prefix = "v"`

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
