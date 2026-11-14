# Flags and prerequisites

Read this page before your first run, or when the skill stops with its usage
line. It gives the two flags, the rules for the plan path, the state that the
repository must have, and the place where the skill finds the staged change.
The short overview is [/sdlc:openspec-save](../openspec-save.md).

## Example

    /openspec-save --plan plans/add-widget.md --auto

The skill resolves the relative path `plans/add-widget.md` against the current
working directory. It saves the change, and the commit skill does not ask you to
approve its message. The pr skill still asks for release intent and for approval
of the PR text.

## Syntax

    /openspec-save <plan-path> | --plan <path> [--auto]

The plan path is required. The skill never picks a plan for you: not from the
conversation, not from the newest plan file, and not from the branch name.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--plan <path>` | Path of the plan file. Same as the positional `<plan-path>`. | none |
| `--auto` | Skips the approval question of the commit skill. The pr skill still asks you for release intent, so the run is not unattended. | off |

`--auto` does not skip the release question. The pr skill in auto mode stops
with an error when no release level is given. So this skill never passes
`--auto` to the pr skill. You answer the question.

## Rules for the plan path

- The plan file has an `**OpenSpec-Staging:**` or an `**OpenSpec-Saved:**`
  header line.
- A relative path is resolved against the current working directory. The
  `openspec_save` tool takes an absolute path only.
- You can give the positional path and `--plan` together. The same path in both
  forms is accepted. Different paths stop the skill with the error
  `openspec-save: <plan-path> and --plan name different files.`
- The skill prints its usage line and stops in these cases: no plan path,
  `--plan` with no value, two positional paths, or an option the skill does not
  know.

The skill calls no tool before it stops for one of these reasons.

## Prerequisites

- `/plan` ran with **Create OpenSpec change**, and the plan file has an
  `**OpenSpec-Staging:**` header line.
- The OpenSpec CLI is installed. The tool calls it to move the change.
- You are on the default branch, or on the branch `openspec/<change>`. On any
  other branch the tool stops. On the default branch it creates and switches to
  `openspec/<change>`.
- Tracked files outside `openspec/changes/<change>/` and `.sdlc-v2/` have no
  changes. Commit or stash them first. Untracked files do not count.

## Where the skill finds the staged change

The change is written in the plan phase, but it is not yet in the tracked tree.
It sits under `.sdlc-v2/openspec-staging/<change>/` of the main worktree. When
the skill can find the main worktree, it runs from any worktree of the repo.
Two other cases exist:

- A plan that an older plugin staged has its change under
  `.sdlc-v2/openspec-staging/<change>/` of the active worktree. The skill uses
  that folder when the folder of the main worktree and
  `openspec/changes/<change>/` are both missing.
- When the skill cannot find the main worktree, it uses only the folder of the
  active worktree and prints the warning `Main worktree not found (...)`.

[The merge-first flow](../openspec-save.md#the-merge-first-flow) shows where the
save sits between `/plan` and `/ship`.
