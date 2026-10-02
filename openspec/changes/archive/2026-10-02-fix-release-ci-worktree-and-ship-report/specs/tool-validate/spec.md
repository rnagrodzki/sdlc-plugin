# Spec Delta

## MODIFIED Requirements

### Requirement: Root selection
The tool SHALL read from the main worktree root, except for `dimensions`, `ci_script_drift`, and opted-in `guardrails`, which read the active worktree root.

| Case | Root | Active root cannot be resolved |
|---|---|---|
| `dimensions` | active | falls back to main (no error) |
| `ci_script_drift` | active | falls back to main (no error) |
| `guardrails` with `activeWorktree: true` | active | `InfraError` `resolve active worktree for guardrails activeWorktree:true: <cause>` |
| Every other case | main | n/a |
| Main root cannot be resolved (any action) | n/a | `InfraError` `resolve project root: <cause>` |

#### Scenario: Guardrails from a linked worktree
- **WHEN** the tool runs from a linked worktree whose `.sdlc-v2/config.toml` has guardrail `Bad_ID` and the main worktree's config does not
- **THEN** `action: "guardrails", activeWorktree: true` reports `Bad_ID`
- **AND** `action: "guardrails"` without the flag does not

#### Scenario: Opt-in with no active worktree fails loud
- **WHEN** the tool runs from the main repo's `.git` directory with `action: "guardrails", activeWorktree: true`
- **THEN** the result is an `InfraError` whose message contains `resolve active worktree`
- **AND** `action: "dimensions"` from the same place succeeds

#### Scenario: CI drift from a linked worktree
- **WHEN** CI files were scaffolded only in a linked worktree and the tool runs there with `action: "ci_script_drift"`
- **THEN** `findings` is an empty list

### Requirement: ci_script_drift action
The `ci_script_drift` action SHALL compare each CI file installed by `scaffold_ci` with the version `scaffold_ci` would install and report non-current files as `warning` findings. It SHALL NOT write files.

| State | ID | Message |
|---|---|---|
| Current | none | none |
| Installed version lower, or only the legacy `.js` file exists | `CI_SCRIPT_OUTDATED` | `<path> is outdated (installed v<I>, current v<C>) — run scaffold_ci({force:true}) to update.` |
| Not installed | `CI_SCRIPT_MISSING` | `<path> is not installed (current v<C>) — run scaffold_ci({force:true}) to install.` |

- `path` is the file path relative to the project root, e.g. `.github/workflows/release-on-main.yml`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Embedded payload missing | `InfraError` | `embedded payload "<name>" not found` / update or reinstall the plugin |
| Installed file cannot be read | `InfraError` | `check installed version of CI script <path>: read <file>: <cause>` / check read permission |

#### Scenario: Freshly scaffolded project
- **WHEN** `scaffold_ci` has just run
- **THEN** `findings` is an empty list

#### Scenario: One outdated, one missing
- **WHEN** `.github/workflows/release-on-main.yml` is replaced with `# release-on-main-version: 1` and `.github/workflows/check-changelog.yml` is deleted
- **THEN** there is one `CI_SCRIPT_OUTDATED` and one `CI_SCRIPT_MISSING` finding
- **AND** both messages contain `scaffold_ci({force:true})`
