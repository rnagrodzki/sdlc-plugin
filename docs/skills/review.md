# /sdlc:review

Review your changes across the quality dimensions of your project, and get one verdict with every finding.

## Why use it

You finished a change and you want a thorough review before you open a pull request.
You want to catch problems in more than one quality dimension: security, performance, error handling, documentation.
This skill starts one reviewer for each review dimension of your project, and each reviewer checks only its own concern.
You get one review comment: a summary table, a verdict, and each finding with its file, line and severity.

## Example

Review the commits of your branch against the base branch:

```text
/sdlc:review
```

The skill finds the changed files, selects the dimensions that match them, and runs one reviewer for each dimension in the background. Then it shows the full review comment in the terminal:

- A summary table: one row for each dimension, with the count of findings at each severity.
- A verdict: `APPROVED`, `APPROVED WITH NOTES` or `CHANGES REQUESTED`.
- Each finding: the severity, a title, the file and the line, a description and a suggestion.

Then the skill asks where the review goes: a comment on the open PR, a file in `.sdlc-v2/reviews/`, or the terminal only.

## What you get

- Catch more in one run: each dimension has its own reviewer, which reads only the changed files that match the dimension.
- Read one clean comment: the skill removes duplicates, marks contradictions and corrects wrong severities before you see it.
- See each gap: a reviewer that stalls is stopped, and the comment names its dimension as skipped.
- Decide where the review goes: you see the full comment first, then you post it to the PR, save it or keep it in the terminal.
- Go from findings to fixes: the skill offers `/sdlc:received-review` to fix the findings, or `/sdlc:harden` to strengthen your guardrails.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0a — Resolve instructions | tool | Gets the review list of the `[instructions]` config. A config error stops the run before Step 0. | The list | yes | `--no-instructions` is not set |
| Step 0 — Run `review_prepare` | tool | Calls `review_prepare`, reads the manifest and shows each warning. On a tool error it shows the error and stops. | The manifest with the dimensions and the waves | no | always |
| Step 1 — Dry Run Check | skill | Prints the review plan, cleans up the files of the run and stops. No agent starts. | The review plan | yes | `--dry-run` is passed |
| Step 2 — Wave Dispatch | agent | Starts one background agent for each dimension, wave by wave, with at most `maxParallelDimensions` agents in a wave. | Workers that write their findings to the ledger | no | always, unless `manifest.waves` is empty |
| Step 3 — Poll Ledger Status (per wave) | tool | Polls `ledger_status` for each wave. It stops and skips a worker that stalls or is missing on two polls in a row. | The findings of each worker | no | always, unless `manifest.waves` is empty |
| Step 4 — Consolidate Findings (relocated critique/dedupe pass) | skill | Removes duplicates, marks contradictions and corrects wrong severities. It ignores the findings of a stopped worker. | The consolidated findings | no | always |
| Step 5 — Build and Persist Consolidated Comment | skill | Builds the review comment with the verdict and saves it as `review-comment.md` in the diff directory. | The comment file and the verdict | no | always |
| Step 6 — Display Full Comment Body | skill | Shows the full text of the comment in the terminal, with no summary. | The visible review | no | always |
| Step 7 — Handle Posting | skill | Checks the links in the comment, then posts it to the PR, saves it under `.sdlc-v2/reviews/` or keeps it in the terminal. | A posted comment or a saved review file | no | always. The options depend on the PR and the scope |
| Step 8 — Offer Self-Fix | skill | Offers `fix` (runs `received-review`), `harden` (only when the verdict is `CHANGES REQUESTED`) or `no`. | A `received-review` run, a `harden` run or nothing | yes | the verdict is `CHANGES REQUESTED` or `APPROVED WITH NOTES` |
| Step 9 — Cleanup | tool | Stops the workers that did not finish. Deletes the manifest file. Deletes the diff directory only when no worker could be left running. | A clean working state | no | always, on every end of the run after Step 0a |
| Step 10 — Run instructions | skill | Runs each instruction of the list, then prints `Instructions (review): …`. | The summary line | yes | not `--dry-run`, Step 7 gave a verdict (the run did not stop on an error), Step 9 has finished, and the list is not empty |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:review`. `--base <branch>` sets the base branch. `--dry-run` shows the plan only. `--no-instructions` skips the follow-up instructions. |
| Ends | After Step 10, or after Step 9 when no list runs. A dry run ends after the cleanup of Step 1. A config error at Step 0a stops the run before Step 0. |
| Asks the user | At Step 7: post, save or keep in the terminal. At Step 8: fix, harden or no. An instruction asks only when its question flags allow it. |
| Resume | none |

## More examples

**Preview the review plan:**

```text
/sdlc:review --dry-run
```

The skill prints the plan and stops. The plan shows the base branch, the count of changed files, and each dimension with its files, severity and status. It also lists the changed files that no dimension covers. No reviewer starts.

**Review against another base branch:**

```text
/sdlc:review --base develop
```

The skill compares the current branch with `develop` instead of the auto-detected base branch.

**Review the changes that you did not commit:**

```toml
[review]
scope = "working"
```

Put these lines in `.sdlc-v2/local.toml`, or set the scope with `/sdlc:setup`. The next `/sdlc:review` covers the staged and unstaged changes of tracked files. The skill does not offer to post this review to a PR.

## Reference

- [Flags and settings](./review/settings.md): the 3 flags, the 5 scopes, the count of parallel reviewers, the dimensions, the dry-run plan and the follow-up instructions.
- [Review results](./review/results.md): the comment, the verdict rules, skipped dimensions, the posting options, the fix offer and what stays after a run.
- [Follow-up instructions](../instructions.md): the `[instructions]` config, the merge rule, the question rules and the summary line.

## Related skills

- [/sdlc:received-review](./received-review.md): fixes the findings of this skill.
- [/sdlc:commit](./commit.md): commits the changes before or after the review.
- [/sdlc:ship](./ship.md): runs this skill as its review step.
- [/sdlc:setup](./setup.md): sets the review dimensions and the scope.
- [/sdlc:harden](./harden.md): proposes stronger guardrails, dimensions and instructions after a `CHANGES REQUESTED` verdict.
