# skill-dashboard Specification

## Purpose
The `/sdlc:dashboard` skill opens, reports, or stops the local sdlc dashboard through the `dashboard` MCP tool in one step.

## Requirements

### Requirement: Arguments
The skill SHALL map each argument to one `dashboard` tool call and print one message.

| Arguments | Tool call | Message |
|---|---|---|
| none | `dashboard({action:"ensure", open:true})` | `Dashboard: <url>` |
| `--no-open` | `dashboard({action:"ensure", open:false})` | `Dashboard: <url>` |
| `--status` | `dashboard({action:"status"})` | the tool `summary` |
| `--stop` | `dashboard({action:"stop"})` | the tool `summary` |

#### Scenario: Default run
- **WHEN** the user runs `/sdlc:dashboard`
- **THEN** the skill calls `dashboard` with `action:"ensure"` and `open:true`
- **AND** prints `Dashboard: http://127.0.0.1:7385`

#### Scenario: Second run
- **WHEN** the server is already active and the user runs `/sdlc:dashboard` again
- **THEN** no second server starts and the same URL prints

### Requirement: Tool error
The skill SHALL print the `## Do this` text of a tool error as it is and SHALL stop with no retry.

#### Scenario: Port in use
- **WHEN** the tool returns the port error
- **THEN** the skill prints the `## Do this` text and makes no other call

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
