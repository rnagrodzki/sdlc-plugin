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
