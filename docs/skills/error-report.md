# /error-report

Propose a GitHub issue for an error that an SDLC skill hit. The skill collects
the error context, asks for your consent twice, and files the issue in
`rnagrodzki/sdlc-plugin`. The full command name is `/sdlc:error-report`.

## When to use

- Another SDLC skill hit an error that looks like a defect of the plugin, and it
  offers to report the error. The skill runs by itself in that case. You only
  answer its questions.
- A calling skill could not start `/error-report` and printed the error fields
  for you. You run the command with those fields.
- You want to track a tooling error that is not yet an open issue.

The skill reports only errors that are worth an issue: a failed prepare tool, a
failed `gh` or `git` command, an API error that repeats, a conflict that stays
after one retry, a task that fails after 2 retries, or a build failure that
blocks a wave. It skips user errors without a question: invalid input, HTTP 401,
403 and 404, a user cancel, a lint-only failure, a missing config value and a
`gh` session that is not logged in.

## Syntax

    /sdlc:error-report skill=<skill> step=<step> operation=<operation> error=<errorText> exitOrHttpCode=<code> errorType=<type>

You do not type this line in the normal flow. A calling skill starts
`/error-report` for you. The line is the fallback for a calling skill that cannot
start it.

## Fields

| Field | Description | Required |
|-------|-------------|----------|
| `skill` | The skill that hit the error. It becomes the second label of the issue. | yes |
| `step` | The step or operation that failed. | yes |
| `operation` | What the skill tried to do. | yes |
| `error` | The full error: exit code, message and HTTP status. | yes |
| `exitOrHttpCode` | The exit code or the HTTP status. | no |
| `errorType` | The kind of error, such as script crash, CLI failure or persistent API error. | no |

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 1: Classify | skill | Decides if the error is worth an issue. It skips the user errors and returns to the normal error handling of the calling skill. | The verdict on the error | no | always |
| Step 2: Pre-flight verification | skill | Runs `gh auth status` and checks that the repo has a git remote. When a check fails, it skips the proposal and says nothing. | A ready or skip result | no | the error is worth an issue |
| Step 3: Consent gate 1 (offer) | user | Asks if you want an issue. On `no` it returns to the normal error handling. | Your consent to draft | no | the checks pass |
| Step 4: Call `prepare_orchestrator` | tool | Calls `prepare_orchestrator` with mode `error_report`. The tool writes the manifest and searches for similar open issues. | The manifest path and the similar issues | no | you said yes |
| Step 5: Dispatch the error-report-orchestrator Agent | agent | Fills the issue template from the manifest on a lightweight model. The agent calls neither `gh` nor `git`. | The issue title and body | no | the manifest is ready |
| Step 6: Consent gate 2 (review) | user | Shows the title, the labels and the body. You choose `yes`, `edit` or `cancel`. On `edit` the skill changes the text and shows it again. | The approved issue text | no | the draft is ready |
| Step 6b: Similar open issues | user | Lists open issues with the labels `tooling-error` and the skill name. You choose `comment`, `new` or `cancel`. On `comment` you pick an issue and approve the text, and the skill posts the comment. | The choice to comment or to create, or the posted comment | yes | the search found similar issues |
| Step 7: Create the GitHub issue and return | skill | Runs `gh issue create` with the labels `tooling-error` and the skill name. Then it removes the manifest and returns to the calling skill. | The issue number and URL | no | you approved the text |

### Run facts

| Fact | Value |
|---|---|
| Starts | When another skill sends an issue-worthy error to it, or when you run `/sdlc:error-report` with the error fields. |
| Ends | After Step 7, after a comment posted in Step 6b, or at once when a step skips the proposal or you answer `no` or `cancel`. It then returns to the calling skill. |
| Asks the user | At two gates: the offer in Step 3 and the review in Step 6. It also asks in Step 6b when similar open issues exist. It has no auto mode. |
| Resume | none |

## What the skill creates

The issue goes to `rnagrodzki/sdlc-plugin`. The repo of your project is not the
target. The title has the form `[<skill>] <one-line summary>` and is at most 72
characters long. The labels are `tooling-error` and the name of the calling
skill. The skill creates a missing label and tries again once.

The body comes from the `ToolingError.md` template and has the error context. It
has no AI attribution line.

## Examples

**An error during a ship run:**

`/ship` hits a failed `gh pr create`. It sends the error to `/error-report`. You
see the offer, answer `yes`, read the draft, and answer `yes` again. The skill
prints `GitHub issue created: #<number> — <url>`. The ship run goes on with its
own error handling.

**A similar issue exists:**

The skill finds `#42 — [pr] gh pr create fails`. You choose `comment`. It shows
the comment text and asks you to approve it. After your `yes` it posts the
comment and creates no new issue.

## Prerequisites

- The `gh` CLI is installed and logged in.
- The repo has a git remote.

When a prerequisite is missing, the skill skips the proposal and says nothing.

## Related skills

- [/harden](harden.md) — Runs `/error-report` when its result is a defect of the
  plugin.
- [/commit](commit.md), [/review](review.md), [/pr](pr.md),
  [/execute](execute.md) and [/received-review](received-review.md) — Send an
  error to `/error-report` when they hit an error that is worth an issue.

## Tips and gotchas

- **The skill does not stop the calling skill.** It adds an issue proposal and
  then returns to the normal error handling of the caller.
- **Two gates guard the issue.** The skill creates no issue and no comment
  until you pass both gates and Step 6b.
- **A failed `gh issue create` is tried again once.** The skill creates the
  missing labels first. When the second try fails, the skill prints
  `Could not create GitHub issue: <error>` and returns.
