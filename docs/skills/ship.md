# /sdlc:ship

Go from a plan file, or from changes you already made, to a reviewed pull request with one command.

## Why use it

A change needs many commands before it is a pull request: implement, commit, review, fix the findings, rebase, open the PR.
Each command is a place where the work stops or a step is forgotten.
`/sdlc:ship` runs these steps in a fixed order and saves its progress, so a stopped run continues where it stopped.

## Example

```
/sdlc:ship --plan ~/.claude/plans/auth-redesign.md
```

Ship executes the plan, commits the changes, reviews them, and opens a pull request.
It pauses for your confirmation between steps.

Review findings at or above the review threshold go to `/sdlc:received-review`. Its fixes land in a separate commit before the pull request opens.

At the end, ship prints the step table, the decisions log and the deferred findings.

## What you get

- **One command from plan to pull request.** Execute, commit, review, fix and PR run in a fixed order.
- **Resume a stopped run.** `/sdlc:ship --resume` continues from the saved progress, also in a new session.
- **Fixes before the pull request.** Findings at or above the review threshold go to `/sdlc:received-review`, and its fixes get their own commit.
- **No finding lost.** Findings below the threshold are saved for `/sdlc:deferred`, and a review ledger in the summary accounts for every finding.
- **A preview before you run.** `/sdlc:ship --dry-run` prints the steps and the pauses, and runs nothing.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| execute | agent | Runs the plan wave by wave. | Changed files on the feature branch | no | always, when a plan file is given |
| commit | agent | Stages every change except `.sdlc-v2/`. The `commit-check` tool completes the step when nothing is left to commit. Otherwise an agent writes the commit. Then pushes the branch, best effort. A failed push gives a warning, and the step still succeeds. | The feature commit | no | always |
| review | agent | Reviews the changes across the project review dimensions. | Findings by severity | no | always |
| received-review | agent | Sends the findings at or above the review threshold to `/sdlc:received-review`, which applies the fixes. Ship commits the fixes as a separate commit when `/sdlc:received-review` changed files. Ship defers the findings below the threshold. | Fixes in the working tree | no | the review found issues at or above the threshold |
| rebase | tool | Rebases the branch onto the base branch. The step skips when the branch is already up to date. | A rebased branch | no | after review, when the `rebase` setting is true, the default |
| verify-openspec | tool | Runs the strict OpenSpec validation of the change. | A validation result | yes | in the steps list and a change exists |
| archive-openspec | tool | Asks for consent, syncs the `tasks.md` boxes when boxes are open, and archives the change. | The archived change | no | a change exists |
| harden | skill | Runs `/sdlc:harden` through the Skill tool to propose guardrail edits. | Approved guardrail edits | yes | in the steps list |
| pr | agent | Creates the pull request, or updates the open one. Pushes with a lease when the branch is already on the remote. | The PR URL | no | always |
| verify-pipeline | tool | Polls the CI checks of the PR. An agent fixes a failed run. | The CI verdict | yes | in the steps list |
| await-remote-review | tool | Polls for the review of an automated reviewer. An agent runs `/sdlc:received-review` when the review is actionable. | The remote review verdict | yes | in the steps list |
| learnings-commit | tool | Logs the learnings of the run. It does not touch git. | The learnings log | no | always |
| step instructions | skill | After each step, runs the follow-up instructions of that step from the `[instructions]` config. | One result for each instruction | yes | the step has a list |
| ship instructions | skill | After the last step, runs the `ship` list. | One result for each instruction | yes | the config has a `ship` list |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:ship`. With `--plan <path>` the run starts at execute. Without it the run starts at commit. A config error in `[instructions]` stops the run before the first step. |
| Ends | After the learnings-commit step and the `ship` instruction list. Ship writes the run report, cleans up the run state and prints the summary. |
| Asks the user | Between steps, unless `--auto` is set. With `--auto`, three cases still ask: resume or replace a run in flight on the branch, file a GitHub issue for an execute-step draft, and a critical question of an instruction whose `criticalQuestions` flag is `true`. |
| Resume | `/sdlc:ship --resume` continues from the saved progress of the run. A resumed run first runs the open instructions of the completed steps. |

## More examples

**Ship without prompts**

```
/sdlc:ship --plan tasks/my-plan.md --auto
```

Runs the full pipeline with no confirmation prompt between steps.
Three cases still ask: a run still in flight on the branch, a GitHub issue for an execute-step draft, and a critical question of an instruction that allows it.

**Ship changes you already made**

```
/sdlc:ship
```

No plan is needed. The run skips execute and starts with commit.

**Resume after an interruption**

```
/sdlc:ship --resume
```

Continues from the saved progress in a new session. You do not pass `--plan` again.

## Reference

- [Flags](./ship/flags.md) — every flag, its default, and the flags that do not run the pipeline.
- [Pipeline steps](./ship/pipeline-steps.md) — what each step does, when it skips, and what stops the run.
- [State, summary and run report](./ship/state-and-trace.md) — what a run saves, how it resumes, and what it prints and writes at the end.
- [Tips and gotchas](./ship/tips-and-gotchas.md) — the settings to check before the first run.
- [Follow-up instructions](../instructions.md): the `[instructions]` config, the merge rule, the question rules and the summary line.

## Related skills

- [/sdlc:setup](./setup.md) — Configures the pipeline steps and settings. Run it before the first ship.
- [/sdlc:plan](./plan.md) — Creates the plan file for the execute step.
- [/sdlc:execute](./execute.md) — The first step of the pipeline, when a plan is given.
- [/sdlc:commit](./commit.md) — The commit step. It runs only when the working tree has changes to commit.
- [/sdlc:review](./review.md) — The review step.
- [/sdlc:received-review](./received-review.md) — Fixes the review findings at or above the threshold.
- [/sdlc:harden](./harden.md) — The opt-in `harden` step. It turns clustered review findings into guardrail edits.
- [/sdlc:pr](./pr.md) — The PR step.
- [/sdlc:verify-pipeline](./verify-pipeline.md) — The opt-in CI check after the PR.
- [/sdlc:deferred](./deferred.md) — Triages the findings and issue drafts that a run left open.
