# /sdlc:pr

Open a pull request with a complete description, written from the commits of your branch and published after your `yes`.

## Why use it

Your branch is ready, and the pull request needs a description that a reviewer and a stakeholder can read.
A description that you write at the end of the work is often short, or it covers only the last commit.
`/sdlc:pr` drafts the title and each section from every commit of the branch, and checks the draft before you see it.
You answer `yes`, and the skill creates the PR, or updates the open PR of the branch.

## Example

Commit your work on a feature branch, then run:

```text
/sdlc:pr
```

You see these items in order:

- A release question: set a release level (`patch`, `minor` or `major`), or skip the release.
- The line `Release: minor — publishing will apply label "release:minor"`, when you set a level.
- `PR Title:` and `PR Description:` with the full text.
- The question `Publish this PR as shown?` with `yes`, `edit` or `cancel`.
- After `yes`, the line `Pull request created: <url>`.

## What you get

- **A description of the full branch.** The skill writes each section from every commit of the branch, not from the last commit only.
- **No invented content.** A section with no evidence gets `N/A` or `Not detected`, or the skill asks you a question.
- **One command for a new PR and an open PR.** The skill creates the PR, or updates the open PR of the branch.
- **A release decision on the PR.** The skill records the release level as a `release:<level>` label, with the release notes in the PR body.
- **Control of each publish.** The skill publishes nothing before your `yes`, and a link that fails the check stops the publish.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0 — Plan Mode Check | skill | Stops the run when plan mode is active, because the skill creates or updates a PR. | The go-ahead for the next step | no | always. It stops the run only in plan mode |
| Step 0 (CONSUME): Call `pr_prepare` | tool | Calls `pr_prepare`. Shows each error and stops when `PR_CONTEXT.ok` is `false`. Shows each warning. | `PR_CONTEXT` | no | always |
| Step 1: Consume the Context | skill | Reads the branch, the Jira ticket, the template, the commits and the version data. Holds the release fields that the caller gave. | The facts for the description and the release intent | no | always |
| Step 1b (GATE): Release Intent | skill | Asks for a release level, or for an acknowledged skip, and drafts the release notes. With `--auto` and no release level it stops with an error. | The release level, the release notes and the release source | yes | no `releaseLevel` was given at invocation |
| Step 2 (PLAN): Draft PR Description | skill | Drafts the title and every section of the PR template from the commits of the branch. | The draft title and description | no | always |
| Step 3 (CRITIQUE): Self-review the Draft | skill | Checks the draft against the quality gates. | A list of failing gates | no | always |
| Step 4 (IMPROVE): Revise Based on Critique | skill | Fixes each failing gate, with at most 2 rounds for each gate. Asks the user a question when a business section stays unclear. | The revised draft | no | always |
| Step 5 (DO): Present for Review | skill | Shows the release intent, the title and the description. Asks `yes`, `edit` or `cancel`. | The approved title and description | no | always. `--auto` and `--skip-approval` skip the question |
| Step 6: Create or Update PR | tool | Checks the links in the body with `links_validate`, then calls `pr_apply` to create the PR or update the open PR. | The PR URL | no | always, after `yes` or after a skipped question |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:pr`. `--draft`, `--base <branch>`, `--auto` and `--skip-approval` change the run. |
| Ends | After Step 6 shows `Pull request created` or `Pull request updated` with the URL. A `cancel`, an error or a link violation ends the run with no PR change. |
| Asks the user | At Step 1b for the release level. At Step 2 or Step 4 when a business section stays unclear. At Step 5: `yes`, `edit` or `cancel`. |
| Resume | none |

## More examples

**Open a draft PR against another base branch:**

```text
/sdlc:pr --draft --base develop
```

The skill opens the new PR as a draft against `develop`. Both flags apply only to a new PR. When the branch has an open PR, the skill updates that PR and prints a warning for each flag that it ignored.

**Update the open PR after new commits:**

```text
/sdlc:pr
```

Run the same command again on the same branch. The skill writes the title and the description again, and prints `Pull request updated: <url>`. There is no `--update` flag.

**Open the PR with no question:**

```text
/sdlc:ship --auto
```

`/sdlc:pr --auto` skips the publish question, but it cannot answer the release question. Typed directly, with no release level from a caller, it stops with an error. `/sdlc:ship` decides the release level first, then runs this skill.

## Reference

- [The PR description](./pr/description.md): the sections, your own template, the fill rules, the quality gates and the link check.
- [Flags and the release decision](./pr/flags-and-release.md): the 4 flags, create or update, the release question, each stop and the limits.

## Related skills

- [/sdlc:setup](./setup.md): sets the PR template of your project.
- [/sdlc:commit](./commit.md): commits your changes before the PR.
- [/sdlc:review](./review.md): reviews the changes of the branch.
- [/sdlc:ship](./ship.md): runs this skill as its PR step, with the release level decided before.
- [/sdlc:verify-pipeline](./verify-pipeline.md): analyzes a failed CI run on the PR.
