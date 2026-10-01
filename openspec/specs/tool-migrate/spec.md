# tool-migrate Specification

## Purpose
`migrate` runs one legacy migration per call: config schema check, import from the old `.sdlc/` plugin directory, or move of the old `.sdlc-v2/execution/` state layout to `.sdlc-v2/runs/`. The `setup` skill calls it during its migration step. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input fields and actions
The tool SHALL accept the input fields below and SHALL run exactly the action named in `action`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `action` | string (enum) | yes | one of `config`, `import`, `layout`, e.g. `import` | Migration to run. |
| `dryRun` | bool | optional at call time; missing = `false` | boolean, e.g. `true` | Report what would change; write nothing. |

| Action | What it does | Writes to |
|---|---|---|
| `config` | Checks the config schema version; never rewrites content. | nothing |
| `import` | Copies or merges data from `.sdlc/` into `.sdlc-v2/`. | `.sdlc-v2/` |
| `layout` | Moves `.sdlc-v2/execution/` entries into `.sdlc-v2/runs/`. | `.sdlc-v2/runs/` |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `action` is any other value | `DomainError` | `unknown migrate action "<action>" (sdlc v<version>, commit <sha>); must be one of: config, import, layout` / set `action` to a valid value, or update the plugin |

#### Scenario: Removed action names
- **WHEN** `action` is `jira_templates` or `learnings_log`
- **THEN** the tool returns a `DomainError` whose message contains `unknown migrate action`

### Requirement: Output fields
The tool SHALL return the fields below.

| Field | Meaning |
|---|---|
| `ok` | `true` on every returned result. |
| `action` | The action that ran. |
| `dryRun` | Echo of the input flag; `false` on the layout stat-warning result. |
| `result` | One-line outcome text (see each action). Lists render as `[a b c]`. |
| `changed` | Repo-relative paths written, or that would be written on dry run. Directories end in `/`. |
| `skippedKeys` | `import` only: legacy keys left out, sorted. A key the destination does not allow is `<dest path>: <key>`; a key the user changed is `<dest path>: <key> (already set)`. Omitted when empty. |
| `errors` | Omitted in all current paths. |

#### Scenario: Import with nothing to do
- **WHEN** `.sdlc/` does not exist
- **AND** `action` is `import`
- **THEN** `ok` is `true`
- **AND** `changed` is empty
- **AND** `result` is `up-to-date: nothing to import`

### Requirement: Config action
For `action: "config"` the tool SHALL NOT write any file. It SHALL return `result: "up-to-date"` for a current or never-set-up project and SHALL refuse a stale project.

Dry run and live run SHALL use the same stale check. A project is stale when:

- `.sdlc-v2/config.json` exists without `.sdlc-v2/config.toml`, OR
- `.sdlc-v2/local.json` exists without `.sdlc-v2/local.toml`.

| Mode | Current project | Stale project |
|---|---|---|
| `dryRun: true` | `result: "up-to-date"` | `result: "would-migrate: configmigrate: version stale: TOML config required. Run /setup to initialize."` |
| `dryRun: false` | `result: "up-to-date"` | `InfraError` `config migration failed: configmigrate: version stale: TOML config required. Run /setup to initialize.` / check `.sdlc-v2/config.toml` and `local.toml`, then retry |

- `changed` is always empty.
- There is no JSON-to-TOML conversion; legacy files outside `.sdlc-v2/` (e.g. `.claude/sdlc.json`) are not detected.

#### Scenario: Stale project refused
- **WHEN** `.sdlc-v2/config.json` exists without `.sdlc-v2/config.toml`
- **AND** `dryRun` is `false`
- **THEN** the tool returns an `InfraError` whose message contains `version stale`

#### Scenario: Stale project dry run
- **WHEN** `.sdlc-v2/config.json` exists without `.sdlc-v2/config.toml`
- **AND** `dryRun` is `true`
- **THEN** `ok` is `true`
- **AND** `dryRun` is `true`
- **AND** `result` contains `would-migrate`
- **AND** `.sdlc-v2/config.json` is unchanged

#### Scenario: Stale local file only
- **WHEN** `.sdlc-v2/config.toml` exists
- **AND** `.sdlc-v2/local.json` exists
- **AND** `.sdlc-v2/local.toml` does not exist
- **THEN** a dry run returns a `result` containing `would-migrate`
- **AND** a live run returns an `InfraError` containing `version stale`

#### Scenario: Only legacy file outside .sdlc-v2
- **WHEN** only `.claude/sdlc.json` exists
- **THEN** a live run returns `result: "up-to-date"`

### Requirement: Import config files merge per key
For `action: "import"` the tool SHALL merge `.sdlc/config.*` into `.sdlc-v2/config.toml` and `.sdlc/local.*` into `.sdlc-v2/local.toml` by top-level key. A legacy key SHALL replace a destination key only while the destination value still equals the shipped template's default for that key; the tool SHALL NOT overwrite a key the user changed.

Per legacy top-level key, in this order:

| Destination state | Action | Reported in |
|---|---|---|
| key not allowed in the destination file | not written | `skippedKeys`: `<dest path>: <key>` |
| value equals the legacy value | nothing to do | nothing |
| key missing | legacy value written | `changed` |
| value equals the template default (decoded values compared) | legacy value written | `changed` |
| any other value (user changed it, or the template has no such key) | kept | `skippedKeys`: `<dest path>: <key> (already set)` |

- The template defaults are the shipped `config.toml` / `local.toml` templates that `setup_init` writes, embedded in the binary.
- Source preference per logical file: `.toml` first; `.json` only when no `.toml` source exists.
- The destination is always the `.toml` file, even for a `.json` source.
- `changed` gets `.sdlc-v2/config.toml` or `.sdlc-v2/local.toml` only when at least one key was written (or would be, on dry run).
- A table value is written by splicing only that table's text, so comments outside the replaced table stay byte-for-byte (same rule as `setup_write_sections`). A non-table value, or a key holding a `.`, is set by one whole-file rewrite, which drops the file's comments.
- A whole number is written as a TOML integer (`90`, not `90.0`).
- The source files are never changed or deleted.
- `.sdlc-v2/config.toml` receives only its allowed top-level keys: `version`, `jira`, `commit`, `pr`, `plan`, `execute`. Any other legacy key (e.g. `schemaVersion`, `ship`) is not merged and is listed in `skippedKeys`.
- `.sdlc-v2/local.toml` has no key filter.

#### Scenario: Key not allowed in config.toml
- **WHEN** `.sdlc/config.json` has `schemaVersion`, `ship`, and `jira`
- **THEN** `.sdlc-v2/config.toml` has `jira` and has no `schemaVersion` or `ship`
- **AND** `skippedKeys` is `[".sdlc-v2/config.toml: schemaVersion", ".sdlc-v2/config.toml: ship"]`
- **AND** the project config stays readable

#### Scenario: Only disallowed keys
- **WHEN** `.sdlc/config.json` has only `schemaVersion`
- **THEN** `.sdlc-v2/config.toml` is not written
- **AND** `changed` is empty

#### Scenario: Merge into scaffolded file
- **WHEN** `.sdlc-v2/local.toml` exists without `ship`
- **AND** `.sdlc/local.json` has `ship.bump = "patch"`
- **THEN** `changed` is `[".sdlc-v2/local.toml"]`
- **AND** `.sdlc-v2/local.toml` has `ship.bump = "patch"`

#### Scenario: Existing key kept
- **WHEN** `.sdlc-v2/config.toml` has `version.tagPrefix = "current"`
- **AND** `.sdlc/config.json` has a different `version` and a `jira` key
- **THEN** `version.tagPrefix` stays `current`
- **AND** `jira` is added
- **AND** `skippedKeys` is `[".sdlc-v2/config.toml: version (already set)"]`

#### Scenario: Template default replaced after setup_init
- **WHEN** `setup_init` wrote both templates unchanged
- **AND** `.sdlc/config.json` has `jira.defaultProject = "OLD"` and a `plan` table
- **AND** `.sdlc/local.json` has `ship.bump = "minor"` and `ship.executeWaveInterval = 90`
- **THEN** `changed` is `[".sdlc-v2/config.toml", ".sdlc-v2/local.toml"]`
- **AND** `skippedKeys` is omitted
- **AND** `.sdlc-v2/config.toml` holds the legacy `jira` and `plan` tables
- **AND** `.sdlc-v2/local.toml` holds `executeWaveInterval = 90`
- **AND** every comment and table outside `[jira]`, `[plan.*]`, and `[ship]` is unchanged

#### Scenario: User-changed key kept
- **WHEN** `setup_init` wrote the `config.toml` template and the user changed `jira.defaultProject` to `MINE`
- **AND** `.sdlc/config.json` has `jira.defaultProject = "OLD"`
- **THEN** `.sdlc-v2/config.toml` is unchanged, on dry run and on a live run
- **AND** `changed` is empty
- **AND** `skippedKeys` is `[".sdlc-v2/config.toml: jira (already set)"]`

#### Scenario: TOML source preferred
- **WHEN** `.sdlc/` has both `config.toml` and `config.json`
- **THEN** only the `config.toml` values are merged
- **AND** `changed` has exactly one `.sdlc-v2/config.toml` entry

### Requirement: Import templates and directories
For `action: "import"` the tool SHALL copy each file and directory below from `.sdlc/` to `.sdlc-v2/` only when the source exists and the destination does not.

| Source under `.sdlc/` | Kind | `changed` entry |
|---|---|---|
| `pr-template.md` | file | `.sdlc-v2/pr-template.md` |
| `plan-template.md` | file | `.sdlc-v2/plan-template.md` |
| `jira-templates/` | directory, recursive | `.sdlc-v2/jira-templates/` |
| `learnings/` | directory, recursive | `.sdlc-v2/learnings/` |
| `review-dimensions/` | directory, recursive | `.sdlc-v2/review-dimensions/` |

- `result`: `imported: [<changed>]`, or `up-to-date: nothing to import` when `changed` is empty.

#### Scenario: Destination already exists
- **WHEN** `.sdlc/pr-template.md` exists
- **AND** `.sdlc-v2/pr-template.md` exists
- **THEN** `.sdlc-v2/pr-template.md` is unchanged
- **AND** `changed` is empty

#### Scenario: Fresh copy
- **WHEN** `.sdlc/config.json` and `.sdlc/jira-templates/template.md` exist
- **AND** `.sdlc-v2/` does not exist
- **THEN** `changed` is `[".sdlc-v2/config.toml", ".sdlc-v2/jira-templates/"]`
- **AND** the `.sdlc/` sources still exist

### Requirement: Import dry run and errors
With `dryRun: true` the import action SHALL report the same `changed` list it would produce and SHALL write nothing. `result` SHALL start with `would-import:` when `changed` is non-empty, else it is `up-to-date: nothing to import`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Legacy config source cannot be parsed | `InfraError` | `read legacy <name>: <cause>` / fix or delete the legacy file, then retry |
| Destination `.toml` cannot be parsed | `InfraError` | `read <name>: <cause>` / fix the TOML, or delete it and re-run `setup_init` |
| Directory create, copy, or merge write fails | `InfraError` | `create .sdlc-v2 directory: …`, `copy <name>: …`, or `merge <name>: …` / check permissions, then retry |

#### Scenario: Dry run writes nothing
- **WHEN** `.sdlc/local.json` has keys missing from `.sdlc-v2/local.toml`
- **AND** `dryRun` is `true`
- **THEN** `changed` is `[".sdlc-v2/local.toml"]`
- **AND** `.sdlc-v2/local.toml` is not written

### Requirement: Layout action moves entries one by one
For `action: "layout"` the tool SHALL move each entry of `.sdlc-v2/execution/` to the same name under `.sdlc-v2/runs/`, one entry at a time. It SHALL merge `.sdlc-v2/execution/ledger/` into `.sdlc-v2/runs/ledger/` child by child.

- Entries are top-level state files, per-run directories, and `ledger/` children.
- A name that already exists at the destination is left at the source, not overwritten, and reported as skipped.
- Other entries still move after a conflict.
- `changed` labels: `.sdlc-v2/runs/<name>` or `.sdlc-v2/runs/ledger/<name>`, with `/` added for directories.
- The action is idempotent; a second run finds nothing to move.

Result text:

| Outcome | `result` |
|---|---|
| Moved only | `migrated: [<changed>]` |
| Moved and skipped | `migrated: [<changed>]; skipped (name conflict): [<skipped>]` |
| Skipped only | `up-to-date: skipped (name conflict): [<skipped>]` |
| Nothing to move (`execution/` missing or empty) | `up-to-date: no legacy execution/ layout to migrate` |

- Skipped labels have no trailing `/`.
- On dry run, `migrated` becomes `would-migrate` and nothing moves.

#### Scenario: Top-level state file moved
- **WHEN** `.sdlc-v2/execution/` holds one state JSON file
- **THEN** the file is under `.sdlc-v2/runs/`
- **AND** the source file is gone

#### Scenario: Ledger merged into existing ledger
- **WHEN** `.sdlc-v2/runs/ledger/` already has other run directories
- **AND** `.sdlc-v2/execution/ledger/<runID>/` exists
- **THEN** `<runID>/` moves into `.sdlc-v2/runs/ledger/`
- **AND** the existing run directories are unchanged

#### Scenario: Name conflict
- **WHEN** a name exists in both `.sdlc-v2/execution/` and `.sdlc-v2/runs/`
- **THEN** the destination is unchanged
- **AND** the source entry stays in place
- **AND** `result` contains `skipped (name conflict)` and that entry's label

#### Scenario: No legacy layout
- **WHEN** `.sdlc-v2/execution/` does not exist
- **THEN** `ok` is `true`
- **AND** `result` is `up-to-date: no legacy execution/ layout to migrate`

### Requirement: Layout errors
The layout action SHALL refuse to move an entry when it cannot tell whether the destination exists.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `.sdlc-v2/execution` stat fails (not "not found") | none (result) | `ok: true`, `result: "warning: cannot stat legacy <path>: <cause> — skipping layout migration"` |
| `.sdlc-v2/execution` or its `ledger/` cannot be listed | `InfraError` | `read <path>: <cause>` / check read permission |
| Destination stat fails (not "not found") | `InfraError` | `cannot determine if destination <path> exists: <cause> — refusing to move to avoid potential overwrite` |
| Parent create or move fails | `InfraError` | `create <dir> directory: …` or `move <path>: …` / check permissions, then retry |

#### Scenario: Unreadable destination
- **WHEN** stat on a destination path fails with permission denied
- **THEN** the tool returns an `InfraError` containing `refusing to move to avoid potential overwrite`

### Requirement: Project root
The tool SHALL resolve the project root to the main worktree root and fall back to the current working directory when that fails.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root and current directory both unresolvable | `InfraError` | `resolve project root: <cause>` / run from an existing directory inside a git repository |

- Annotations: `Title: "Migrate SDLC config"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: true`, `OpenWorld: false`.

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs from a linked git worktree
- **THEN** `.sdlc/` and `.sdlc-v2/` are resolved under the main worktree root
