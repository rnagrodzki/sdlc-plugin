# Spec Delta

## ADDED Requirements

### Requirement: Setup tab in the T3 preview tab
When the `dashboard` output has `previewUrl`, the setup skill SHALL open the Setup tab with the T3 `preview_open` tool.

#### Scenario: Setup tab in T3 Code
- **WHEN** the user picks the dashboard Setup tab in a T3 thread
- **THEN** the skill calls `preview_open` with `previewUrl`

#### Scenario: Setup tab fails
- **WHEN** `preview_open` fails
- **THEN** the skill prints the address and one warning line and continues
