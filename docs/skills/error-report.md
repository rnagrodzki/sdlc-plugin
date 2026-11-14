# /sdlc:error-report

Turn a plugin error into a GitHub issue with the full error context, and approve the text before the skill files it.

## Why use it

A skill of the plugin fails, and the error looks like a defect of the plugin, not of your project.
A report by hand costs time: you copy the error, the step, the branch and the command into an issue.
This skill collects that context, writes the issue draft and shows the draft to you.
It files the issue in `rnagrodzki/sdlc-plugin` only after you answer `yes` two times.

## Example

`/sdlc:pr` hits a `pr_prepare` tool error that looks like a plugin defect, and it starts this skill for you.
When a skill cannot start it, the skill gives you a line of this form, and you run the line:

```text
/sdlc:error-report skill=<skill> step=<step> operation=<operation> error=<errorText> exitOrHttpCode=<code> errorType=<type>
```

In both cases you see these lines, and `/sdlc:pr` then continues with its own error handling:

- The offer: `This error may be worth tracking as a GitHub issue. Create one? (yes / no)`
- After `yes`, the draft: the title, the labels `tooling-error` and `pr`, the description, and `Create this issue? (yes / edit / cancel)`
- After the second `yes`: `GitHub issue created: #<number> — <url>`

## What you get

- **A report with no copy work.** The draft has the skill, the step, the operation, the full error text, the repository, the branch and a timestamp.
- **Control of what is filed.** The skill creates nothing until you accept the offer and then approve the full draft. `edit` changes the text first.
- **No duplicate issues.** When similar open issues exist, you can add the error as a comment to one of them.
- **No offer for your own errors.** The skill skips invalid input, HTTP 401, 403 and 404, a cancel and a lint-only failure without a question.
- **A run that is not blocked.** The skill adds the offer, then returns to the error handling of the calling skill.

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
| Step 7: Create the GitHub issue and return | skill | Runs `gh issue create` with the labels `tooling-error` and the skill name. Then it removes the manifest and returns to the calling skill. | The issue number and URL | no | you approved the text, and Step 6b did not end the run |

### Run facts

| Fact | Value |
|---|---|
| Starts | When another skill sends an issue-worthy error to it, or when you run `/sdlc:error-report` with the error fields. |
| Ends | After Step 7, or after a comment posted in Step 6b. It ends earlier when a step skips the proposal, when you answer `no` or `cancel`, when `prepare_orchestrator` returns an error, or when the skill cannot read the draft. |
| Asks the user | At two gates: the offer in Step 3 and the review in Step 6. It also asks in Step 6b when similar open issues exist. It never creates an issue without both gates. |
| Resume | none |

## More examples

**Add the error to an open issue:**

The skill prints `Similar open issue(s) found:` with a list, such as `#42 — [plan] Task decomposition fails on empty specs`.
Answer `comment`, pick the issue, and approve the comment text.
The skill prints `Comment added to #<number> — <url>` and creates no new issue.

**Change the draft before the skill files it:**

At `Create this issue? (yes / edit / cancel)`, answer `edit` and say what to change.
The skill changes the title or the body and shows the draft again, until you answer `yes` or `cancel`.

**Decline the report:**

Answer `no` to the offer, or `cancel` to the draft.
The skill creates nothing, and the calling skill continues with its own error handling.

## Reference

- [Errors with and without an offer](./error-report/which-errors.md): the 6 kinds of error that get an offer, the 8 kinds that get none, and the 2 prerequisites.
- [Issue and manual command](./error-report/issue.md): the fields of the manual command, the target repository, the title, the labels, the body, the comment and the failure messages.

## Related skills

- [/sdlc:harden](./harden.md): offers this skill when it classifies a failure as a plugin defect.
- [/sdlc:execute](./execute.md): offers this skill after a task fails after 2 retries.
- [/sdlc:commit](./commit.md): starts this skill for a `commit_apply` error that looks like a plugin defect.
- [/sdlc:pr](./pr.md): starts this skill for a `pr_prepare` or `pr_apply` error that looks like a plugin defect.
- [/sdlc:review](./review.md): starts this skill for a tool-call crash, not for a user error.
- [/sdlc:received-review](./received-review.md): starts this skill when a reply to GitHub fails two times with a server error.
