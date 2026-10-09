# /openspec-save

Save the OpenSpec change that `/plan` staged as its own branch, commit, and pull
request. Reviewers read and merge the spec first. You ship the implementation
after that. The full command name is `/sdlc:openspec-save`.

## When to use

- `/plan` ran with **Create OpenSpec change**, and the plan file has an
  `**OpenSpec-Staging:**` header line.
- You want the spec reviewed and merged before any code exists.
- A run of `/openspec-save` stopped half way, and you want to continue it.

## The merge-first flow

The change is written in the plan phase, but it is not yet in the tracked tree.
It sits under `.sdlc-v2/openspec-staging/<change>/`. This skill moves it to the
repository and sends it to review on its own:

1. `/plan` stages the change and writes the `**OpenSpec-Staging:**` header line
   in the plan file.
2. `/openspec-save` creates the branch `openspec/<change>`, moves the change into
   `openspec/changes/<change>/`, commits it, and opens a PR.
3. You and your reviewers merge that PR into the default branch.
4. You create a new branch from the default branch and run `/ship` with the same
   plan file.

The OpenSpec PR and the code PR are two separate PRs.

## Syntax

    /openspec-save <plan-path> | --plan <path> [--auto]

The plan path is required. The skill never picks a plan for you.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--plan <path>` | Path of the plan file. Same as the positional `<plan-path>`. If you give both forms with different paths, the skill stops with an error. The same path in both forms is accepted. | none |
| `--auto` | Skips the approval question of the commit skill. The pr skill still asks you for release intent, so the run is not unattended. | off |

A relative path is resolved against the current working directory. The
`openspec_save` tool takes an absolute path only.

## What the skill does

| Step | Action |
|------|--------|
| 1. Save | Calls the `openspec_save` tool. It creates the branch, moves the change into `openspec/changes/<change>/`, stamps a ref comment into each task line of `tasks.md`, stages the change, and rewrites the plan header to `**OpenSpec-Saved:**`. It does not commit. |
| 2. Commit | Runs the commit skill with `--type docs --scope openspec`, and with `--auto` when you passed it. It runs only when the tool reports staged files. |
| 3. PR | Runs the pr skill. It asks you for release intent. Choose **Skip release (acknowledged)**: the PR holds only the OpenSpec change. |
| 4. Report | Prints the change, the branch, and the PR URL. It tells you to merge the PR and then run `/ship`. |

The commit skill and the pr skill run in your session, so their questions reach
you directly.

## Examples

**Save the change of a plan:**

    /openspec-save /Users/me/.claude/plans/add-widget.md

Creates `openspec/add-widget`, commits the change after you approve the commit
message, and opens a PR after you answer the release-intent question.

**Name the plan with a flag:**

    /openspec-save --plan /Users/me/.claude/plans/add-widget.md

Same result. Use this form when you want the argument to be explicit.

**Skip the commit approval:**

    /openspec-save /Users/me/.claude/plans/add-widget.md --auto

The commit skill does not ask you to approve its message. The pr skill still asks
for release intent and for approval of the PR text.

## Prerequisites

- The OpenSpec CLI is installed. The tool calls it to move the change.
- You are on the default branch, or on the branch `openspec/<change>`. On any
  other branch the tool stops. On the default branch it creates and switches to
  `openspec/<change>`.
- Tracked files outside `openspec/changes/<change>/` and `.sdlc-v2/` have no
  changes. Commit or stash them first. Untracked files do not count.

## Related skills

- [/plan](plan.md) — Stages the OpenSpec change and writes the header line that
  this skill reads.
- [/commit](commit.md) — Commits the staged change. It runs inside this skill.
- [/pr](pr.md) — Opens the PR, or updates the open PR of the branch. It runs
  inside this skill.
- [/ship](ship.md) — Run it on a new branch from the default branch after the
  OpenSpec PR is merged.

## Tips and gotchas

- **Merge the OpenSpec PR before you ship.** Run `/ship` on a new branch from
  the default branch after the merge. Do not run it on `openspec/<change>`.
- **`--auto` does not skip the release question.** The pr skill in auto mode
  stops with an error when no release level is given. So this skill never passes
  `--auto` to the pr skill. You answer the question.
- **A stopped run is safe to repeat.** After the save, the plan has the
  `**OpenSpec-Saved:**` header line. A second run of `/openspec-save` makes the
  tool return `already`. The tool then stages nothing and changes no file. The
  run continues where it stopped:
    - The commit did not happen: the change is still staged, and the run
      continues at the commit.
    - The commit happened, but the PR did not: nothing is staged, so the commit
      is skipped and the run continues at the PR. The commit skill stops when
      nothing is staged, with the error "no files staged for commit".
    - A PR for the branch is already open: the pr skill updates that PR.
- **Switch to the saved branch before you run it again.** On a second run the
  tool does not check the current branch. The skill checks it and stops when it
  is not the saved branch.
- **An error stops the run.** The skill prints the error and stops in these
  cases:
    - The call has a usage error: no plan path, two different plan paths, or an
      option the skill does not know. The skill calls no tool first.
    - The tool returns an error. The skill prints the error and its Suggestion.
    - The commit skill or the pr skill returns an error. The skill also reports
      the branch.
- **Decline stops the run.** If you decline the commit, the skill stops and
  reports the branch and the staged change. If you decline the PR, it stops and
  reports the branch.
- **Do not edit the plan header by hand.** The tool rewrites it. A plan with both
  an `**OpenSpec-Staging:**` and an `**OpenSpec-Saved:**` line is an error.
