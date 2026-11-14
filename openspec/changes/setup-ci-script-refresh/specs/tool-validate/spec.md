# Spec Delta

## MODIFIED Requirements

### Requirement: ci_script_drift action
The `ci_script_drift` action SHALL compare each CI file installed by `scaffold_ci` with the version `scaffold_ci` would install and report non-current files as `warning` findings. It SHALL NOT write files.

| State | ID | Message |
|---|---|---|
| Current | none | none |
| Installed version lower, or only the legacy `.js` file exists | `CI_SCRIPT_OUTDATED` | `<path> is outdated (installed v<I>, current v<C>) — run scaffold_ci({updateOutdated:true}) to update.` |
| Not installed | `CI_SCRIPT_MISSING` | `<path> is not installed (current v<C>) — run scaffold_ci({updateOutdated:true}) to install.` |

- `path` is the file path relative to the project root, e.g. `.github/workflows/release-on-main.yml`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Embedded payload missing | `InfraError` | `embedded payload "<name>" not found` / update or reinstall the plugin |
| Installed file above 1048576 bytes | `DataError` | `check installed version of CI script <path>: ...larger than 1048576 bytes` / restore the file with git or delete it, then run the same call again |
| Installed file cannot be read | `InfraError` | `check installed version of CI script <path>: read <file>: <cause>` / check read permission |

#### Scenario: Freshly scaffolded project
- **WHEN** `scaffold_ci` has just run
- **THEN** `findings` is an empty list

#### Scenario: One outdated, one missing
- **WHEN** `.github/workflows/release-on-main.yml` is replaced with `# release-on-main-version: 1` and `.github/workflows/check-changelog.yml` is deleted
- **THEN** there is one `CI_SCRIPT_OUTDATED` and one `CI_SCRIPT_MISSING` finding
- **AND** both messages contain `scaffold_ci({updateOutdated:true})`

#### Scenario: Installed file above the limit
- **WHEN** one installed CI file is 1048577 bytes
- **THEN** the result is a `DataError`
