# /sdlc:received-review

Get each review comment on your pull request checked against the code, then fixed or answered with a technical reason.

## Why use it

A reviewer, human or automated, left comments on your pull request. Some comments are correct, some are wrong, and some are unclear.
To agree with every comment is fast, and it puts wrong changes into your code.
This skill checks each comment against the full codebase and gives it a verdict: will fix, will not fix, disagree or needs direction.
You approve the plan. Then the skill applies the fixes and replies in each comment thread.

## Example

Work through the review comments of pull request 42:

```text
/sdlc:received-review --pr 42
```

The skill reads the comments and checks each one against the code, its callers and its tests. It changes nothing at this point. It shows you:

- A summary table: one row for each comment, with its file, line, type, verdict and a one-line reasoning.
- The action plan: the comments it will fix, the comments it will push back on, and the comments that need direction.
- The full text of each drafted reply.

Answer `implement`, `edit` or `skip`. After `implement`, the skill applies the fixes one at a time and checks each fix. It then asks before it posts the replies. The run ends with a ledger line such as `Review findings: 14 total = 9 fixed + 5 deferred`.

## What you get

- Catch a wrong comment before it changes your code: the skill reads the code, the callers and the tests behind each comment.
- Keep control: the skill changes no code and posts no reply before you approve, unless you pre-approve a severity or pass `--auto`.
- Send replies that start with the substance: one reply in each comment thread, with no praise. The threads stay open for you to resolve.
- Lose no finding: a ledger accounts for every finding, and each finding that the skill records as unfixed goes to the `/sdlc:deferred` backlog.
- Turn findings into guardrails: the skill groups the findings by file and offers a `/sdlc:harden` run for each group.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0a — Resolve instructions | tool | Gets the received-review list of the `[instructions]` config. A config error stops the run before Step 0. | The list | yes | `--no-instructions` is not set |
| Step 0 — Plan Mode Check | skill | Stops the run when plan mode is active, because the skill writes files and posts replies. | The go-ahead for the next step | no | always. It stops the run only in plan mode |
| Step 1 — READ: Gather Review Feedback | skill | Reads `--auto`, `--no-harden` and `--pr` from the arguments and keeps them for the later steps. | The parsed flags | no | always |
| Step 1a — Fetch PR overview and the auto-apply severities (every run) | tool | Calls `received_review_prepare` for the PR overview, the CI status and `alwaysFixSeverities`. | The PR overview and the pre-approved severities | no | always. With no PR number the overview is skipped |
| Step 1b — Fetch actual comments (always required — the tool above does not return them) | tool | Fetches the reviewer comments with `gh` and parses each one into a list. Skips the comments that are already addressed and records why. | The list of feedback items | no | always |
| Step 2 — UNDERSTAND: Categorize and Flag | skill | Gives each item a type and flags the unclear items. Without `--auto` it asks about all unclear items at once. With `--auto` it records each one as `needs-direction`. | The typed items | no | always |
| Step 3 — VERIFY: Check Against Full Codebase Context | skill | Checks each item against the code, the callers and the tests, and sets a verification status. | The verified items | no | always |
| Step 4 — EVALUATE: Assess Each Item | skill | Sets a verdict for each item: agree will fix, agree won't fix, disagree or needs direction. With `--auto` only agree will fix and needs direction can remain. | The verdicts | no | always |
| Step 5 — CRITIQUE #1: Self-Critique the Evaluation | skill | Checks the evaluation against the gates. It shows nothing to the user. | A list of failing gates | no | always |
| Step 6 — IMPROVE #1: Revise Evaluation | skill | Fixes each failing gate, with at most 2 rounds for each gate. | The revised evaluation | no | always |
| Step 7 — RESPOND (DO): Draft Responses | skill | Drafts one reply for each item. Each reply starts with the substance and has no praise. | The drafted replies | no | always |
| Step 8 — CRITIQUE #2: Self-Critique the Responses | skill | Checks the replies against the gates. It shows nothing to the user. | A list of failing gates | no | always |
| Step 9 — IMPROVE #2: Revise Responses | skill | Fixes each failing gate of the replies, with at most 2 rounds for each gate. | The revised replies | no | always |
| Step 10 — PRESENT: Show Findings and Proposed Plan | skill | Shows the summary table, the action plan and the drafted replies. Asks `implement`, `edit` or `skip`. | The approved findings | no | always. `--auto` skips the question, and findings in `alwaysFixSeverities` need no approval |
| Step 11 — IMPLEMENT: Execute Changes | skill | Applies the code fixes in order of importance, records the fix progress and records every finding that stays unfixed. | The fixes and the records of unfixed findings | no | there is an approved finding |
| Step 11.6 — META-ANALYZE: Cluster Findings and Dispatch harden | skill | Groups the findings that have a verdict and runs `harden` for each approved group through the Skill tool. A failure here does not stop the run. | A `harden` run | yes | `--no-harden` is not passed, and `AskUserQuestion` or `--auto` is available |
| Step 11.7 — LINK VERIFICATION — HARD GATE | tool | Checks every URL in the reply bodies with `links_validate`. On a violation the skill posts no reply and stops. | The link check result | no | always, before replies are posted |
| Step 12 — REPLY: Post PR Thread Replies | tool | Prints the summary and the ledger. Without `--auto` it asks `yes`, `skip` or `selective`. Then it posts each reply in its comment thread with `gh api`. | The posted replies | no | always. With no PR number only the summary and the ledger are printed |
| Step 12.5 — VERIFY: Confirm All Threads Replied | tool | Calls `received_review_verify` and replies to each thread that is still outstanding, in at most 2 rounds. | The final thread status | no | a PR number exists |
| Step 13 — Run instructions | skill | Runs each instruction of the list, then prints `Instructions (received-review): …`. | The summary line | yes | no cancel at Step 10, and the list is not empty |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:received-review`. `--pr <number>` selects the PR. `--auto`, `--no-harden` and `--no-instructions` change the run. |
| Ends | After Step 13. When no list runs, after Step 12.5, or after Step 12 when there is no PR number. A config error at Step 0a stops the run before Step 0. A link violation at Step 11.7 stops the run. |
| Asks the user | At Step 2 for unclear items, at Step 3 for items that cannot be verified, at Step 10: `implement`, `edit` or `skip`, at Step 11.6: `dispatch` or `skip` for each group, and at Step 12: `yes`, `skip` or `selective`. `--auto` skips these prompts. An instruction asks only when its question flags allow it. |
| Resume | none |

## More examples

**Fix the findings of a local review, with no pull request:**

```text
/sdlc:received-review
```

Run it after `/sdlc:review` in the same conversation, or paste the findings. With no PR number, the skill takes the feedback from the conversation or from your paste. It fixes what you approve, then prints the summary and the ledger. It posts no reply, because there is no comment thread.

**Run with no finding prompt, in a pipeline:**

```text
/sdlc:received-review --pr 42 --auto
```

The skill fixes every "will fix" finding and asks nothing about the findings. It records each other finding as `needs-direction` for `/sdlc:deferred`, and it does not close that finding. A follow-up instruction still asks a critical question when its `criticalQuestions` flag allows it. See [Auto mode](./received-review/consent-and-auto-mode.md#auto-mode).

**Pre-approve the severities that you always fix:**

```toml
# .sdlc-v2/local.toml
[receivedReview]
alwaysFixSeverities = ["critical", "high"]
```

A "will fix" finding with a listed severity gets its fix with no prompt, and one `pre-approved` line shows it. One prompt asks about the other findings. See [Consent and auto-apply severities](./received-review/consent-and-auto-mode.md#consent-and-auto-apply-severities).

## Reference

- [Flags, consent and auto mode](./received-review/consent-and-auto-mode.md): the 4 flags, the prompts of a manual run, the pre-approved severities, what `--auto` changes, the follow-up instructions, and tips.
- [Records and the ledger](./received-review/records-and-ledger.md): the ledger terms, where an unfixed finding is recorded, and the fix progress on the dashboard.
- [Follow-up instructions](../instructions.md): the `[instructions]` config, the merge rule, the question rules and the summary line.

## Related skills

- [/sdlc:review](./review.md): produces the findings that this skill responds to.
- [/sdlc:commit](./commit.md): commits the fixes after the run.
- [/sdlc:ship](./ship.md): runs this skill when review findings are at or above the review threshold.
- [/sdlc:deferred](./deferred.md): triages the findings that this skill records and does not fix.
- [/sdlc:harden](./harden.md): turns a group of findings into project guardrails.
