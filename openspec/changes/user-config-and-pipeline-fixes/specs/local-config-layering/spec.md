# Spec Delta

## Purpose

Lets a developer keep personal settings (the `local.toml` sections) in one user-level file that every project reads, with the project's `.sdlc-v2/local.toml` overriding it key by key.

## ADDED Requirements

### Requirement: User file location
The system SHALL use `$SDLC_USER_CONFIG` as the user file path when that variable is set, else `~/.sdlc/local.toml`.

- The user file uses the same sections as `.sdlc-v2/local.toml` and the same schema.
- A missing user file is an empty layer, not an error.

#### Scenario: Environment override
- **WHEN** `SDLC_USER_CONFIG` is `/tmp/u/local.toml`
- **THEN** the system reads personal settings from `/tmp/u/local.toml` and `.sdlc-v2/local.toml`

#### Scenario: Default path
- **WHEN** `SDLC_USER_CONFIG` is unset and the home directory is `/home/a`
- **THEN** the user file is `/home/a/.sdlc/local.toml`

#### Scenario: User file absent
- **WHEN** the user file does not exist
- **THEN** personal settings equal the values of `.sdlc-v2/local.toml`
- **AND** no error is returned

### Requirement: No user layer without a home directory
When `SDLC_USER_CONFIG` is unset and no home directory is available, the system SHALL read personal settings from `.sdlc-v2/local.toml` only and SHALL return no error.

#### Scenario: No home directory
- **WHEN** `SDLC_USER_CONFIG` is unset and no home directory is available
- **THEN** personal settings equal the values of `.sdlc-v2/local.toml`
- **AND** no error is returned

### Requirement: Deep merge with project precedence
The system SHALL merge the user file under `.sdlc-v2/local.toml`: tables merge key by key at every depth, the project value wins on a conflict, and a list or a scalar in the project file replaces the user value whole.

#### Scenario: Key in both files
- **WHEN** the user file has `[style] audience = "functional"` and `tone = "direct"`
- **AND** `.sdlc-v2/local.toml` has `[style] audience = "technical"`
- **THEN** the merged `[style]` is `audience = "technical"` and `tone = "direct"`

#### Scenario: Section only in the user file
- **WHEN** the user file has `[review] scope = "diff"`
- **AND** `.sdlc-v2/local.toml` has no `[review]` section
- **THEN** the merged `review.scope` is `diff`

#### Scenario: Nested table
- **WHEN** the user file has `[automation.report] enabled = false` and `format = "json"`
- **AND** `.sdlc-v2/local.toml` has `[automation.report] format = "md"`
- **THEN** the merged `automation.report` is `enabled = false` and `format = "md"`

#### Scenario: List replaced whole
- **WHEN** the user file has `[ship] steps = ["execute", "commit", "review", "pr"]`
- **AND** `.sdlc-v2/local.toml` has `[ship] steps = ["execute", "commit"]`
- **THEN** the merged `ship.steps` is `["execute", "commit"]`

### Requirement: Every reader sees the merged values
Every skill and tool that reads a `local.toml` section (`style`, `planStyle`, `review`, `receivedReview`, `ship`, `github`, `automation`, `executePrefs`, `workspace`) SHALL get the merged values of the user file and `.sdlc-v2/local.toml`.

#### Scenario: Review scope from the user file
- **WHEN** the user file has `[review] scope = "committed"`
- **AND** `.sdlc-v2/local.toml` has no `[review]` section
- **THEN** `review_prepare` uses scope `committed`

#### Scenario: Ship setting from the user file
- **WHEN** the user file has `[ship] draft = true`
- **AND** `.sdlc-v2/local.toml` has a `[ship]` table without `draft`
- **THEN** `ship_prepare` uses `draft = true`

### Requirement: Section absent only when absent from both files
The system SHALL treat a `local.toml` section as absent only when neither the user file nor `.sdlc-v2/local.toml` has it.

#### Scenario: Section in neither file
- **WHEN** neither file has a `[github]` section
- **THEN** the `[github]` section is absent and each reader uses its default or its absent-section behavior

#### Scenario: Section in the user file only
- **WHEN** only the user file has a `[github]` section
- **THEN** the `[github]` section is present

### Requirement: User file errors
The system SHALL return an error that names the user file path when the user file cannot be read as TOML or is a directory.

| Failure | Error text | Suggestion |
|---|---|---|
| User file has bad TOML | `config: <user path>: <parse error>` | `Fix the TOML syntax in <user path>, or unset SDLC_USER_CONFIG.` |
| User path is a directory | `config: <user path>: is a directory` | `Point SDLC_USER_CONFIG to a file.` |

- A bad `.sdlc-v2/local.toml` keeps its current error.

#### Scenario: Malformed user file
- **WHEN** `SDLC_USER_CONFIG` is `/tmp/u/local.toml`
- **AND** that file holds invalid TOML
- **THEN** the error starts with `config: /tmp/u/local.toml:`
- **AND** the Suggestion is `Fix the TOML syntax in /tmp/u/local.toml, or unset SDLC_USER_CONFIG.`

#### Scenario: User path is a directory
- **WHEN** `SDLC_USER_CONFIG` is `/tmp/u`, a directory
- **THEN** the error is `config: /tmp/u: is a directory`
- **AND** the Suggestion is `Point SDLC_USER_CONFIG to a file.`

### Requirement: Messages name both files
A user-facing error or warning about a `local.toml` section SHALL name both files as `.sdlc-v2/local.toml (or ~/.sdlc/local.toml)`.

- This covers the `[review]` read error and the `maxDimensions` error of `review_prepare`, the `[github]` warnings of `pr_prepare`, and the `reviewThreshold` error of `ship_prepare`.

#### Scenario: Invalid review threshold
- **WHEN** the merged `[ship] reviewThreshold` is `urgent`
- **THEN** the `ship_prepare` error ends with `in [ship] of .sdlc-v2/local.toml (or ~/.sdlc/local.toml)`

#### Scenario: Unreadable github section
- **WHEN** the `[github]` section cannot be read
- **THEN** the `pr_prepare` warning starts with `.sdlc-v2/local.toml (or ~/.sdlc/local.toml) [github] section unreadable:`

### Requirement: Project config unchanged
The system SHALL keep reading the `.sdlc-v2/config.toml` sections (`version`, `jira`, `commit`, `pr`, `plan`, `execute`) from `.sdlc-v2/config.toml` as before; the user layer applies only to `local.toml` sections.

#### Scenario: Project section read as before
- **WHEN** a user file exists
- **AND** `.sdlc-v2/config.toml` has `[jira] defaultProject = "PROJ"`
- **THEN** `jira.defaultProject` is `PROJ`

#### Scenario: Project config read error unchanged
- **WHEN** a user file exists
- **AND** `.sdlc-v2/config.toml` holds invalid TOML
- **THEN** the error and its Suggestion are the same as with no user file
