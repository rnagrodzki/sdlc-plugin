# Errors with and without an offer

Read this page when you want to know why `/sdlc:error-report` offered an issue for one error and gave no offer for another. For the value of the skill and its steps, see the [overview](../error-report.md).

Two examples:

- `gh pr create` fails with an error that is not an auth error. The skill offers an issue.
- A call fails with HTTP 401. The skill gives no offer, because the auth token is expired and you must act.

## Errors that get an offer

The skill offers an issue for these 6 kinds of error.

| Error | Example |
|---|---|
| Prepare-tool failure | A `*_prepare` tool of the plugin returns a domain error or an infra error. |
| CLI tool failure | `gh pr create` or `gh pr edit` fails with an error that is not an auth error. `git tag` or `git push` fails. |
| Persistent API error | HTTP 400 or 5xx on the same operation of an external API, 2 or more times in a row. |
| Persistent conflict | HTTP 409 that stays after one retry. |
| Escalated task failure | A task in `/sdlc:execute` fails after 2 retries. |
| Build failure that blocks the run | The build fails and blocks the next wave. |

## Errors that get no offer

The skill skips these 8 kinds of error without a message. The calling skill continues with its own error handling.

| Error | Reason |
|---|---|
| A validation error from a prepare tool: missing or invalid input | Your input: a missing config value or wrong arguments. |
| HTTP 401 | The auth token is expired. You must act. |
| HTTP 403 | The permission is not sufficient. You must act. |
| HTTP 404 on an issue key | A typing error, not a defect. |
| You cancel | You chose this. It is not an error. |
| A lint-only failure | The severity is low, and a tool can fix it. |
| A missing project key or config value | Your setup, not a defect. |
| `gh auth` is not logged in | Your setup, not a defect. |

## Prerequisites

The skill does 2 checks before the offer:

- `gh auth status` passes. The `gh` CLI works, and you are logged in.
- `git remote get-url origin` returns a URL. The repository has a remote with the name `origin`.

When a check fails, the skill skips the proposal and prints nothing.

The remote is only a check. The issue always goes to `rnagrodzki/sdlc-plugin`, not to the repository of your project.
