# Stops and repeat runs

Read this page when a run stopped, with an error or because you declined a
question, and you want to know what the repository holds and how to continue.
The short overview is [/sdlc:openspec-save](../openspec-save.md).

## Example

    /openspec-save /Users/me/.claude/plans/add-widget.md

You ran this command before, and you declined the commit. The repository is on
`openspec/add-widget`, and the change is staged. Run the same command again on
that branch. The tool returns `already`, and the run continues at the commit.

## What a save changes

Step 1 calls the `openspec_save` tool. The tool does this work:

1. On the default branch, it creates the branch `openspec/<change>` and switches
   to it.
2. It moves the change into `openspec/changes/<change>/`.
3. It stamps a ref comment into each task line of `tasks.md`.
4. It stages the change, and it [checks the files](#the-file-check-after-git-add).
5. It rewrites the plan header `**OpenSpec-Staging:**` to `**OpenSpec-Saved:**`.

The tool does not commit. A change with no `tasks.md` is saved with a warning,
and the skill prints it. The commit skill and the pr skill run in your session,
so their questions reach you directly.

## Run the skill again

A stopped run is safe to repeat. After the save, the plan has the
`**OpenSpec-Saved:**` header line. A second run of `/openspec-save` makes the
tool return `already`. The tool then runs `git add` on the change folder again
and changes no file. The run continues where it stopped:

| State after the stop | What the next run does |
|---|---|
| The commit did not happen | The change is staged, and the run continues at the commit. This is also true when you unstaged the change, for example with `git reset`. |
| The commit happened, but the PR did not | The change folder is the same as in the last commit, so the commit is skipped and the run continues at the PR. The commit skill stops when nothing is staged, with the error "no files staged for commit". |
| A PR for the branch is already open | The pr skill updates that PR. It does not open a second PR. |
| The tool stopped with an error after it created `openspec/<change>` | The repository is on that branch, and the next run continues the save there. |

Switch to the saved branch before you run the skill again. On a second run the
tool checks that the current branch is the saved branch and that
`openspec/changes/<change>/` exists. When a check fails, the tool returns an
error and the run stops.

## What stops a run

| Cause | What the skill does | What you do |
|---|---|---|
| A usage error in the call | Prints the usage line or the error, and calls no tool. | Correct the call. See [Rules for the plan path](./flags-and-prerequisites.md#rules-for-the-plan-path). |
| The tool returns an error | Prints the error and its Suggestion, and reports the current branch. | For a `domain` error, correct the input or the repository state. For an `infra` error, correct the environment, for example file access or the OpenSpec CLI. Then run the skill again. |
| You decline the commit | Stops. Reports the branch and the staged, uncommitted change. | Run the skill again when you are ready. |
| The commit skill returns an error | Prints the error and reports the branch. | Correct the cause, then run the skill again. |
| You decline the PR | Stops. Reports the branch. | Run the skill again when you are ready. |
| The pr skill returns an error | Prints the error and reports the branch. | Correct the cause, then run the skill again. |

## The file check after `git add`

The tool checks the files after `git add`. It checks that each file of
`stage.json` is on disk and in the git index. It also checks that no file under
`openspec/changes/<change>/` is untracked. `.openspec.yaml` is never reported.
A gap returns a `domain` error that names each path, and the plan header stays
`**OpenSpec-Staging:**`:

- A file is not in the index, or is untracked: run
  `git add openspec/changes/<change>/` and run `/openspec-save` again.
- A file is missing on disk: delete `openspec/changes/<change>/`, keep the
  `**OpenSpec-Staging:**` header in the plan, and run `/sdlc:plan` to stage the
  change again.

After a first call, the staging folder is gone. A second call checks only for
untracked files.

## The plan header

- Do not edit the plan header by hand. The tool rewrites it. A plan with both an
  `**OpenSpec-Staging:**` and an `**OpenSpec-Saved:**` line is an error.
- Save with the plan that staged the change. When the `stage.json` of the
  staging folder names another plan file, the tool returns a `domain` error that
  names the owner plan, and no file changes.

## After the PR is open

Merge the OpenSpec PR before you ship. Run `/ship` on a new branch from the
default branch after the merge. Do not run it on `openspec/<change>`.
