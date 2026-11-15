# Spec Delta

## MODIFIED Requirements

### Requirement: Output fields
The tool SHALL return the fields below and SHALL always return `ok: true` when it returns a result.

| Field | Meaning |
|---|---|
| `ok` | Always `true` on a returned result. |
| `needsMigration` | `true` when the config-migration check fails. See "Config-migration flag". |
| `sections` | 19 section descriptors in canonical order. See "Section manifest". |
| `defaultBranch` | Detected default branch. Omitted when not detected. |
| `remoteOwner` | Owner parsed from the `origin` remote URL. Omitted when not detected. |
| `ciScriptDrift` | One entry per CI script managed by `scaffold_ci`. Always present; may be empty. |
| `ciScriptDriftError` | The drift comparison error and its Suggestion. Omitted when the comparison works. See "CI script drift". |
| `userConfigPath` | Absolute path of the user file (`$SDLC_USER_CONFIG`, else `~/.sdlc/local.toml`); `""` when no home directory is available. See "Personal settings values". |
| `localValues` | Merged personal settings per local setup section, with the source file of each key. See "Personal settings values". |
| `explanation` | Only with `explain`: `option`, `label`, `type`, `options`, `default`, `description`, `details`, `examples`, `configFile`, `configPath`, `consumedBy`. |
| `next` | Only with `explain`: tells the skill to show the explanation and ask the open question again. |

- With `explain`, the result holds only `ok`, `explanation` and `next`.
- An empty `options` list renders as `(none)`, never as a blank line.

#### Scenario: Fresh temp directory
- **WHEN** the tool runs in an empty directory that is not a git repository
- **THEN** `ok` is `true`
- **AND** `sections` is non-empty
- **AND** the first section has a non-empty `id`
- **AND** the first section has a non-empty `label`

#### Scenario: Explain one option
- **WHEN** the caller passes `{"explain":"ship.rebase"}`
- **THEN** `explanation.option` is `ship.rebase`
- **AND** `explanation.details` is non-empty
- **AND** `explanation.examples` has 1 to 3 entries
- **AND** `sections` is omitted

#### Scenario: Explain output has no personal settings
- **WHEN** the caller passes `{"explain":"ship.rebase"}`
- **THEN** `userConfigPath` and `localValues` are omitted

### Requirement: CI script drift
The tool SHALL report one `ciScriptDrift` entry per CI script or workflow that `scaffold_ci` installs, comparing the version installed under the active worktree root with the version embedded in the running binary. It SHALL NOT write any file.

| Field | Meaning |
|---|---|
| `script` | Destination path of the script or workflow. |
| `installedVersion` | Version found in the installed file; `0` when not installed. |
| `currentVersion` | Version embedded in the binary. |
| `action` | `current`, `outdated`, or `missing`. |

- `outdated`: installed version is lower than current, or only the legacy file exists.
- `missing`: neither the destination nor the legacy file exists.
- When the active worktree root cannot be resolved, the comparison uses the project root.
- If the comparison fails, `ciScriptDrift` is `[]`, `ciScriptDriftError` holds the error message and its Suggestion, and the tool still returns `ok: true`. The text is the message, a period when the message has none, a space and the Suggestion; with no Suggestion it is the message alone.
- An installed file above 1048576 bytes makes the comparison fail with the size error of the `tool-scaffold-ci` spec.
- Remediation is `scaffold_ci({updateOutdated:true})`; see the `tool-scaffold-ci` spec.

#### Scenario: Nothing scaffolded
- **WHEN** no CI script is installed in the project
- **THEN** every `ciScriptDrift` entry has `action: "missing"`
- **AND** `installedVersion: 0`

#### Scenario: Scaffolded in a linked worktree
- **WHEN** `scaffold_ci` ran in a linked worktree and `setup_prepare` runs from the same linked worktree
- **THEN** every `ciScriptDrift` entry has `action: "current"`

#### Scenario: Installed file above the limit
- **WHEN** one installed CI file is 1048577 bytes
- **THEN** `ok` is `true`
- **AND** `ciScriptDrift` is `[]`
- **AND** `ciScriptDriftError` contains `Restore the file with git or delete it.`

#### Scenario: Drift after an update
- **WHEN** `.github/workflows/release-on-main.yml` holds `# release-on-main-version: 1`
- **AND** `scaffold_ci` runs with `updateOutdated: true` between two `setup_prepare` calls
- **THEN** the first call reports that script with `action: "outdated"`
- **AND** the second call reports it with `action: "current"`
