# Flags

Read this page when you want to change which steps a `/sdlc:ship` run does, how often it asks, or how it opens the pull request. No flag is required.

## Example

```
/sdlc:ship --plan tasks/my-plan.md --auto --draft
```

Runs the full pipeline with no confirmation prompt between steps, and opens the pull request as a draft.

## Syntax

```
/sdlc:ship [options]
```

## All flags

| Flag | Description | Default |
|------|-------------|---------|
| `--plan <path>` | Plan file to execute. Omit to skip the execute step and start from commit. | none |
| `--auto` | Suppress all confirmation prompts for the whole run. Two exceptions still ask: whether to resume or replace a run that is still in flight on this branch — see [State and Resolution Trace](./state-and-trace.md#state-and-resolution-trace) — and filing a GitHub issue for an execute-step draft — see [End-of-run summary](./state-and-trace.md#end-of-run-summary). | off |
| `--steps <csv>` | Comma-separated set of steps to run, overriding the project's step table. The plugin sorts them into the fixed order. | from config |
| `--quick` | Run the steps set to true in the project's `[ship.quick]` table. Does nothing if no quick table is configured. | off |
| `--quality <level>` | Quality tier forwarded to the execute step: `full`, `balanced`, or `minimal`. | unset (execute decides) |
| `--bump <level>` | Release bump forwarded to the PR step: `patch`, `minor`, `major`, or a pre-release label. | `patch` |
| `--draft` | Create the PR as a draft. Has no effect when the branch already has an open PR: that PR is updated, not converted. | off (or from config) |
| `--dry-run` | Print the pipeline table (steps, arguments, pauses) and stop without running anything. | off |
| `--resume` | Resume from saved progress after an interruption. | off |
| `--gc` | Clean up old saved-progress files and stop. Does not run the pipeline. | off |
| `--ttl-days <N>` | Retention period, in days, used by `--gc`. | `14` |
| `--openspec-change <name>` | The OpenSpec change to validate and archive during the pipeline. | auto-detected |
| `--init-config` | Redirects you to `/sdlc:setup`. Does not run the pipeline. | off |

## Flags that do not run the pipeline

Three flags stop before any step runs:

- `--dry-run` prints the pipeline table and stops.
- `--gc` cleans up old saved-progress files and stops. `--ttl-days <N>` sets its retention period.
- `--init-config` sends you to [/sdlc:setup](../setup.md).

## Related pages

- [Pipeline steps](./pipeline-steps.md) — the steps that `--steps` and `--quick` select.
- [Tips and gotchas](./tips-and-gotchas.md) — `--quick` and `--auto` behaviour that surprises people.
- [Overview of /sdlc:ship](../ship.md)
