---
name: openspec-save
description: "Use this skill to save the OpenSpec change that plan staged, as its own branch, commit, and PR, before you ship the implementation. It calls the openspec_save tool, then the commit skill, then the pr skill. Merge that PR first, then run ship on a new branch from the default branch. Arguments: <plan-path> | --plan <path> [--auto]. Use --auto to skip the commit approval question. The pr skill always asks. Triggers on: save openspec, openspec PR, spec PR first."
user-invocable: true
argument-hint: "<plan-path> | --plan <path> [--auto]"
model: sonnet
---

# OpenSpec Save (SDLC)

`/sdlc:plan` with the choice **Create OpenSpec change** stages the change files under `.sdlc-v2/openspec-staging/<change>/`. That folder is outside the tracked tree. This skill moves the change onto its own branch `openspec/<change>`, commits it, and opens a PR. Reviewers then review the spec before any code exists. Merge that PR first. Then run `/sdlc:ship` on a new branch from the default branch.

**Announce at start:** "I'm using openspec-save (sdlc v{sdlc_version})." - extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

**Communication style:** Follow the `sdlc communication style` block in session context for every explanation, status line, summary, and AskUserQuestion text in this skill. Do not apply it to commit messages, PR bodies, review comments, or Jira text: they follow their own templates and config.

**One session.** This skill runs the commit skill and the pr skill with the Skill tool, in this session. Their approval questions reach the user directly. This skill starts no Agent worker.

---

## Arguments

| Argument | Meaning |
|---|---|
| `<plan-path>` | Path of the plan file that has an `**OpenSpec-Staging:**` or `**OpenSpec-Saved:**` header line. Required, unless `--plan` gives it. |
| `--plan <path>` | Same as `<plan-path>`. |
| `--auto` | Skips the approval question of the commit skill. The pr skill still asks the user for release intent. |

Rules for the plan path:

1. The plan path is required. Never infer it from the conversation, from the newest plan file, or from the branch name.
2. The positional path and `--plan` may both be given. With the same path, accept it. With different paths, stop with the error `openspec-save: <plan-path> and --plan name different files.` Call no tool first.
3. Stop with the usage line `/sdlc:openspec-save <plan-path> | --plan <path> [--auto]` when no plan path is given, when `--plan` has no value, when two positional paths are given, or when any other argument appears. Call no tool first.
4. The `openspec_save` tool accepts an absolute path only. Resolve a relative path against the current working directory. Compare the two forms after this step.

---

## Step 1 — Save

Call the tool with the absolute plan path (the positional `<plan-path>` or the `--plan` value):

```
openspec_save({ planPath: "<absolute plan path>" })
```

The result is Markdown with these fields: `change`, `branch`, `branchCreated`, `materialized` (`created` or `already`), `refsStamped`, `stagedFiles`, `summary`, `next`. An empty list renders as `(none)`.

The tool runs only on the default branch or on the branch `openspec/<change>`. Tracked files outside `openspec/changes/<change>/` and `.sdlc-v2/` must have no changes. On the default branch it creates and switches to `openspec/<change>`. It moves the staged change into `openspec/changes/<change>/`, writes a ref comment into each task line of `tasks.md`, and runs `git add` on the change folder. It rewrites the plan header `**OpenSpec-Staging:**` to `**OpenSpec-Saved:**`. It does not commit.

**On tool error:** print the error message and its Suggestion as they are. Stop.

**On success:** print `summary`. Then do the branch check below when it applies. Then route on `stagedFiles`. Do not route on `next`. The tool returns one of two `next` texts: "Run the commit skill with --type docs, then the pr skill." or "Nothing is staged for the change. Run the pr skill if the branch has no pull request yet."

**Branch check, first, when `materialized` is `already`:** the plan was saved by an earlier run. With the header `**OpenSpec-Saved:**`, the tool does not check the current branch. Run `git branch --show-current` every time `materialized` is `already`. When the result differs from `branch`, stop. Tell the user to run `git switch <branch>` and then run this skill again.

**Route on `stagedFiles`, after the branch check:**

- `stagedFiles` has paths: go to Step 2.
- `stagedFiles` is `(none)`: the change is already committed. Skip Step 2. Go to Step 3.

---

## Step 2 — Commit

Run this step only when `stagedFiles` has paths.

```
Skill("sdlc:commit", "--type docs --scope openspec [--auto when --auto was passed]")
```

Add `--auto` only when the user passed `--auto` to this skill. The commit skill drafts the message and asks the user to approve it. With `--auto` it skips that question.

The commit skill stops with an error when nothing is staged: `commit_prepare` returns "no files staged for commit". This is the reason to skip Step 2 when `stagedFiles` is `(none)`.

- The user declines the commit: stop. Report the branch and the staged, uncommitted change under `openspec/changes/<change>/`.
- The commit skill stops with an error: print its error. Stop. Report the branch.

---

## Step 3 — PR

Print one line before the call: "The pr skill asks for release intent next. This PR holds only the OpenSpec change. Choose **Skip release (acknowledged)**." The user answers the question. Never answer it for the user.

```
Skill("sdlc:pr", "")
```

Never pass `--auto` to the pr skill, also when `--auto` was passed to this skill. The pr skill in auto mode stops with an error when no release level is given. Without `--auto` it asks for release intent and asks the user to approve the PR text.

When an open PR exists for the branch, the pr skill updates it. It does not open a second PR.

- The user declines the PR: stop. Report the branch.
- The pr skill fails: print its error. Stop. Report the branch.

---

## Step 4 — Report

Print these items:

- `change`: the change name.
- `branch`: the branch that holds the change.
- PR URL: the URL from the pr skill result.
- Next: merge the PR. Then create a new branch from the default branch and run `/sdlc:ship --plan <plan-path>`. Do not run ship on this branch before the merge.

---

## Routes

| Situation | Exit |
|---|---|
| Tool error | Print the error and its Suggestion. Stop. |
| `materialized` is `already` and the current branch differs from `branch` (checked before `stagedFiles`) | Stop. Tell the user to run `git switch <branch>` and run this skill again. |
| `stagedFiles` is `(none)` | The change is already committed. Skip Step 2. Go to Step 3. |
| User declines the commit | Stop. Report the branch and the staged, uncommitted change. |
| Step 2 ran, and the commit skill stops with an error, or the pr skill fails | Print its error. Stop. Report the branch. |
| User declines the PR | Stop. Report the branch. |
| `--auto` | The commit skill skips its approval question. The pr skill still asks for release intent. |
| Success | Step 4 report. |

## Run the skill again

A stop is safe to repeat. The saved plan header makes `openspec_save` return `already`. The tool then stages nothing and changes no file. The next run continues where the last run stopped:

- The commit did not happen: `stagedFiles` still has paths. The run continues at Step 2.
- The commit happened, but the PR did not: `stagedFiles` is `(none)`. The run skips Step 2 and continues at Step 3.
- An open PR exists for the branch: the pr skill updates that PR.

---

## Rules

- Run the commit skill and the pr skill with the Skill tool. Start no Agent worker.
- Never pass `--auto` to the pr skill.
- Never answer a question of the commit skill or the pr skill for the user.
- Never run `git add`, `git commit`, `git push`, or `gh` by hand. The tool and the two skills do this work. The one git command of this skill is the read-only `git branch --show-current` in Step 1.
- Never edit the plan file by hand. The tool rewrites its header.
- `--auto` does not make this skill unattended. The pr skill always asks.

Related: [`/plan`](../plan/SKILL.md) stages the change. [`/commit`](../commit/SKILL.md) and [`/pr`](../pr/SKILL.md) run inside this skill. [`/ship`](../ship/SKILL.md) implements the plan after the OpenSpec PR is merged.
