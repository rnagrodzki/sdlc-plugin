# Spec Delta

## MODIFIED Requirements

### Requirement: Input and output fields
The tool SHALL accept two input fields and return the output fields below.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `force` | boolean | optional at call time; missing = `false` | `true` or `false` | Overwrite every file that already exists, current ones too, and migrate legacy `.js` files |
| `updateOutdated` | boolean | optional at call time; missing = `false` | `true` or `false` | Overwrite only files whose installed version is below the bundled version, migrate legacy `.js` files and create missing files; current files stay unchanged |

| Field | Meaning |
|---|---|
| `root` | Absolute path of the worktree the files were written under |
| `warnings` | Warning strings; an empty list when there are none |
| `files` | One entry per manifest file, in manifest order: `path`, `action`, `installedVersion`, `currentVersion`, `group` |
| `protection` | Branch protection report: `hasRulesets`, `hasClassicProtection`, `defaultBranch`, `rulesetNames`, `notes` |
| `next` | Next-step guidance (see the guidance requirement) |

- Annotations: `Title: "Scaffold CI workflow files"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: true`, `OpenWorld: true`. `OpenWorld` is `true` because the branch protection check runs `gh api`.

#### Scenario: Force omitted
- **WHEN** the caller passes `{}`
- **THEN** the tool runs with `force: false`
- **AND** the advertised input schema does not list `force` as required

#### Scenario: updateOutdated omitted
- **WHEN** the caller passes `{}`
- **THEN** the tool runs with `updateOutdated: false`
- **AND** the advertised input schema does not list `updateOutdated` as required

#### Scenario: Fresh project
- **WHEN** the tool runs with `force: false` in a project with no `.github/` files
- **THEN** `files` has 8 entries, each with `action: created`
- **AND** `warnings` is an empty list

### Requirement: Per-file action
The tool SHALL choose one `action` per manifest file from the table below, and SHALL write the file only for `created`, `overwritten`, and `migrated`.

| New path exists | Legacy path exists | `force` | `updateOutdated` | Installed < current | `action` | Effect |
|---|---|---|---|---|---|---|
| no | yes | true | false | any | `migrated` | Delete legacy file, write new file |
| no | yes | false | true | any | `migrated` | Delete legacy file, write new file |
| no | yes | false | false | any | `outdated` | Warning `Legacy file found: <legacy> → run scaffold_ci with updateOutdated true to migrate to <path>` |
| no | no | any | any | n/a | `created` | Write new file (parent folders created) |
| yes | any | true | false | any | `overwritten` | Write new file |
| yes | any | false | true | yes | `overwritten` | Write new file |
| yes | any | false | true | no | `skipped` | None |
| yes | any | false | false | yes | `outdated` | Warning `<path> is outdated (installed: v<I>, current: v<C>). Run scaffold_ci with updateOutdated true to update.` |
| yes | any | false | false | no | `skipped` | None |

#### Scenario: Second run without force skips
- **WHEN** the tool runs twice with `force: false`
- **THEN** every entry of the second run has `action: skipped`

#### Scenario: Second run with force overwrites
- **WHEN** the tool runs once, then again with `force: true`
- **THEN** every entry of the second run has `action: overwritten`

#### Scenario: Legacy file without force
- **WHEN** `.github/scripts/retag-release.js` exists and `.github/scripts/retag-release.cjs` does not, and `force` is `false`
- **THEN** the `.github/scripts/retag-release.cjs` entry has `action: outdated`
- **AND** the legacy file is not deleted

#### Scenario: Legacy file migrated with force
- **WHEN** the same legacy file exists and `force` is `true`
- **THEN** the entry has `action: migrated`
- **AND** `.github/scripts/retag-release.js` is deleted and `.github/scripts/retag-release.cjs` exists

#### Scenario: updateOutdated updates an outdated file
- **WHEN** `.github/workflows/release-on-main.yml` holds `# release-on-main-version: 1`
- **AND** the tool runs with `updateOutdated: true`
- **THEN** the entry of that file has `action: overwritten`

#### Scenario: updateOutdated keeps a current file
- **WHEN** `.github/workflows/release-on-main.yml` holds the current version marker and a local edit
- **AND** the tool runs with `updateOutdated: true`
- **THEN** the entry of that file has `action: skipped`
- **AND** the local edit is still in the file

#### Scenario: Second run with updateOutdated skips
- **WHEN** the tool runs twice with `updateOutdated: true`
- **THEN** every entry of the second run has `action: skipped`

### Requirement: Infrastructure errors
The tool SHALL stop and return an `InfraError` on the file-system failures below. Files written before the failure stay on disk.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Active worktree root cannot be resolved | `InfraError` | `resolve project root: <cause>` / run `git rev-parse --show-toplevel`, fix, retry |
| Embedded payload missing | `InfraError` | `embedded payload "<name>" not found` / update or reinstall the plugin |
| Installed file cannot be read | `InfraError` | `check installed version of CI script <path>: read <file>: <cause>` / check read permission |
| Legacy file cannot be deleted | `InfraError` | `remove legacy <file>: <cause>` / delete it by hand, rerun with the same input |
| Folder cannot be created | `InfraError` | `mkdir <dir>: <cause>` / remove the blocking file, make the parent writable |
| File cannot be written | `InfraError` | `write <file>: <cause>` / check write permission and disk space |

#### Scenario: Unwritable destination
- **WHEN** a destination file cannot be written
- **THEN** the result is an `InfraError` whose message starts with `write `

## ADDED Requirements

### Requirement: Conflicting overwrite inputs
The tool SHALL return a `DomainError` and SHALL NOT read or write any file when both `force` and `updateOutdated` are `true`.

#### Scenario: Both inputs true
- **WHEN** the caller passes `{ "force": true, "updateOutdated": true }` in a project with no `.github/` files
- **THEN** the result is a `DomainError` with the message `force and updateOutdated cannot both be true`
- **AND** no file exists under `.github/`

### Requirement: Atomic file writes
The tool SHALL write each file to a temporary file in the destination folder and then rename it over the destination. A failed write SHALL leave the old file content in place.

- Exception: a migration deletes the legacy `.js` file before the write. A failed migration write leaves neither file, and the next run creates the new file.

#### Scenario: Write fails in a read-only folder
- **WHEN** `.github/workflows/` has mode `0555` and holds `release-on-main.yml` with `# release-on-main-version: 1`
- **AND** the tool runs with `updateOutdated: true`
- **THEN** the result is an `InfraError`
- **AND** `release-on-main.yml` keeps its old content

### Requirement: Installed file size limit
The tool SHALL read at most 1048576 bytes (1 MiB) of each installed CI file. A larger file SHALL give a `DataError` in every mode, with the Suggestion `The installed file is larger than 1 MiB, so it is not a plugin CI file. Restore the file with git or delete it. Then run the same call again.`

#### Scenario: File at the limit
- **WHEN** an installed CI file is exactly 1048576 bytes
- **THEN** the tool reads the file and chooses an action for it

#### Scenario: File above the limit
- **WHEN** an installed CI file is 1048577 bytes
- **AND** the tool runs with `force: true`
- **THEN** the result is a `DataError`
- **AND** the file is unchanged
