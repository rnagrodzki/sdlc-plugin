## MODIFIED Requirements

### Requirement: Import config files merge per key
For `action: "import"` the tool SHALL merge `.sdlc/config.*` into `.sdlc-v2/config.toml` and `.sdlc/local.*` into `.sdlc-v2/local.toml` by top-level key. A legacy key SHALL replace a destination key only while the destination value still equals the shipped template's default for that key; the tool SHALL NOT overwrite a key the user changed.

Per legacy top-level key, in this order:

| Destination state | Action | Reported in |
|---|---|---|
| key not allowed in the destination file | not written | `skippedKeys`: `<dest path>: <key>` |
| legacy value is not a table, or the key holds a `.` | not written | `skippedKeys`: `<dest path>: <key> (not a section)` |
| value equals the legacy value | nothing to do | nothing |
| key missing | legacy value written | `changed` |
| value equals the template default (decoded values compared) | legacy value written | `changed` |
| any other value (user changed it, or the template has no such key) | kept | `skippedKeys`: `<dest path>: <key> (already set)` |

- The template defaults are the shipped `config.toml` / `local.toml` templates that `setup_init` writes, embedded in the binary.
- Source preference per logical file: `.toml` first; `.json` only when no `.toml` source exists.
- The destination is always the `.toml` file, even for a `.json` source.
- `changed` gets `.sdlc-v2/config.toml` or `.sdlc-v2/local.toml` only when at least one key was written (or would be, on dry run).
- Each value is written with the same key-level writer as `setup_write_sections`: only the lines of changed keys change, every comment line stays, and missing template tips of the written key are restored.
- When a destination section cannot be edited in place and the file has a comment line, import does not change that file and returns a `DomainError` (see "Import dry run and errors"). A destination file with no comment line falls back to a whole-file rewrite; that fallback is not reported.
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
- **AND** every comment line of both templates is still in the files, including the `defaultProject` tip between `[jira]` and `defaultProject = 'OLD'`

#### Scenario: Non-section legacy key skipped
- **WHEN** `setup_init` wrote the `local.toml` template unchanged
- **AND** `.sdlc/local.json` is `{"version":2,"ship":{"bump":"minor"}}`
- **THEN** `.sdlc-v2/local.toml` holds `ship.bump = "minor"` and no top-level `version`
- **AND** `skippedKeys` is `[".sdlc-v2/local.toml: version (not a section)"]`
- **AND** every comment outside `[ship]` is unchanged

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

### Requirement: Import dry run and errors
With `dryRun: true` the import action SHALL report the same `changed` list it would produce and SHALL write nothing. `result` SHALL start with `would-import:` when `changed` is non-empty, else it is `up-to-date: nothing to import`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Legacy config source cannot be parsed | `InfraError` | `read legacy <name>: <cause>` / fix or delete the legacy file, then retry |
| Destination `.toml` cannot be parsed | `InfraError` | `read <name>: <cause>` / fix the TOML, or delete it and re-run `setup_init` |
| Directory create, copy, or merge write fails | `InfraError` | `create .sdlc-v2 directory: …`, `copy <name>: …`, or `merge <name>: …` / check permissions, then retry |
| A destination section cannot be edited in place and the file has a comment line | `DomainError` | `merge <name>: <cause>` / `Edit section <key> in <name> by hand, then retry migrate with action "import".` |

#### Scenario: Dry run writes nothing
- **WHEN** `.sdlc/local.json` has keys missing from `.sdlc-v2/local.toml`
- **AND** `dryRun` is `true`
- **THEN** `changed` is `[".sdlc-v2/local.toml"]`
- **AND** `.sdlc-v2/local.toml` is not written

#### Scenario: Import refused to keep comments
- **WHEN** import must write key `jira` into `.sdlc-v2/config.toml`
- **AND** that file has a comment line
- **AND** the `jira` section cannot be edited in place
- **THEN** the tool returns a `DomainError` whose Suggestion is `Edit section jira in .sdlc-v2/config.toml by hand, then retry migrate with action "import".`
- **AND** `.sdlc-v2/config.toml` is unchanged
