# Spec Delta

## ADDED Requirements

### Requirement: Open the T3 preview tab
When the `dashboard` output has `previewUrl`, the dashboard skill SHALL open it with the T3 `preview_open` tool.

#### Scenario: Preview tab opens
- **WHEN** `/sdlc:dashboard` runs in a T3 thread and `previewUrl` is set
- **THEN** the skill calls `preview_open` with `url` set to `previewUrl`

#### Scenario: Preview tab fails
- **WHEN** `preview_open` fails or is not available
- **THEN** the skill prints the address and one warning line
- **AND** no system browser opens and the skill does not stop with an error

#### Scenario: Status, stop and no-open
- **WHEN** the flag is `--status`, `--stop` or `--no-open`
- **THEN** the skill makes no `preview_open` call
