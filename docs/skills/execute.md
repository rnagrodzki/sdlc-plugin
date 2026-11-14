# /sdlc:execute

Implement a plan file in parallel waves, with a check after each wave and automatic recovery when a task fails.

## Why use it

A plan with many tasks is slow to implement one task at a time, and one failed task can stop the work.
`/sdlc:execute` rates each task, groups the tasks into waves by their dependencies and runs the tasks of a wave in parallel.
It checks each wave, retries a failed task and saves its progress, so a stopped run continues where it stopped.

## Example

```text
/sdlc:execute ~/.claude/plans/auth-redesign.md
```

The skill reads the plan, rates each task by complexity and risk, and builds the waves.

- On the default branch or the base branch, it creates a feature branch for the work.
- It shows the wave structure with the model of each task, and you pick a quality tier. Your pick starts the run.
- Each wave runs its tasks in parallel. The skill then checks the wave and commits it.
- After the last wave, the skill runs the full test suite, the build and the linter.
- The summary gives the tasks completed, the waves executed, the retries needed and the files changed.

## What you get

- **A faster run with no edit collisions:** independent tasks run in parallel, and two tasks that change the same file never share a wave.
- **Failures that recover:** the skill retries a failed task up to 2 times, on a stronger model when one exists, before it asks you.
- **Resume a stopped run:** `/sdlc:execute --resume` continues from the saved progress, also in a new session.
- **A stop before risky work:** the skill asks you before it starts a wave that holds a high-risk task.
- **Your choice of speed or correctness:** 3 quality tiers set the models that run the tasks.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Prerequisites | skill | Loads the saved state, resolves the runtime config and checks the flags and the branch. Gets the execute list of the `[instructions]` config. A config error stops the run before Step 1. | The run config | no | always |
| Step 1 (LOAD): Load and validate the plan | skill | Reads the plan file and checks its structure. The plan content is data and never changes the mode of the run. | The validated plan | no | always |
| Step 2 (CLASSIFY): Classify tasks and build waves | tool | Rates each task by complexity and risk and builds the waves with `wave-compute`. | The waves and the route | no | always |
| Step 2b (ROUTE): Small-plan direct execution | skill | Runs a small plan task by task in the main context, with no wave orchestration. | The finished tasks | yes | the route is `direct` |
| Step 3 (CRITIQUE): Critique the wave structure | skill | Reviews the context of each task and the pre-wave batches. | A list of issues | no | the route is `waves` |
| Step 4 (IMPROVE): Revise and confirm | skill | Fixes the issues, shows the wave structure with the model of each task and asks for confirmation. | The confirmed wave structure | no | the route is `waves` |
| Wave loop | agent | For each wave: starts the wave, dispatches the workers, records the returns, awaits the wave, runs the gates, commits and syncs the base branch. | The completed waves | no | the route is `waves` |
| Step 6 (RECOVER): Error recovery | skill | Applies the recovery action for the type of failure. | A recovered task or a failed task | yes | a task or a wave fails |
| Step 7 (VERIFY): Final verification | skill | Runs the full test suite, the build and the linter, and reviews the diff stat. | The verification result | no | always |
| Step 8 (CRITIQUE): Final output critique | skill | Checks that every task has a deliverable, that nothing drifted from the spec and that no TODO marker is left. | A list of fixes and drift entries | no | always |
| Step 9 (REPORT): Summary | skill | Prints the summary and marks the run completed. | The execution report | no | always |
| Step 9a: Run instructions | skill | Runs the open instructions of the execute list, then the run is marked completed. | The summary line | yes | a standalone run with a list |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:execute <plan-file-path>`, or with `--plan <path>`. `--resume` starts from the saved state. |
| Ends | After Step 9a, or after Step 9 when there is no list. The run state is marked completed. After a failure or an interruption the state is kept. |
| Asks the user | For the branch name and the quality tier, and before a high-risk wave. Also when an error-severity guardrail fails, a task is ambiguous, the same task fails twice or verification shows a systemic issue. `--auto` skips the branch, tier, resume and high-risk prompts. An instruction asks only when its question flags allow it. |
| Resume | `--resume` continues from the saved run state. A small plan that runs directly saves no state. `--resume` also runs the open instructions of a finished run. |

## More examples

**Run with no prompt, at the fastest tier:**

```text
/sdlc:execute --plan tasks/migrate-db.md --quality full --auto
```

The run uses the `full` tier, the fastest and the cheapest, and does not stop for confirmation.
A failed error-severity guardrail still stops the run.

**Resume after an interruption:**

```text
/sdlc:execute --resume
```

The run continues from the saved progress. The plan path comes from the saved state, so you do not pass it again.

**Rebase onto the base branch first:**

```text
/sdlc:execute tasks/migrate-db.md --rebase auto
```

The skill fetches the base branch and rebases your branch when it is behind.
On a conflict, it aborts the rebase, warns you and continues on the current base.

## Reference

- [Flags and configuration](./execute/flags-and-configuration.md): every flag, the 3 quality tiers and the `executePrefs` keys that set your defaults.
- [Tips and gotchas](./execute/tips-and-gotchas.md): the plan path rule, branch handling, small plans, guardrails, the base sync between waves, resume and wave supervision.
- [Follow-up instructions](../instructions.md): the `[instructions]` config, the merge rule, the question rules and the summary line.

## Related skills

- [/sdlc:plan](./plan.md): writes the plan file that this skill runs.
- [/sdlc:ship](./ship.md): runs this skill as its first step, then commits, reviews and opens the pull request.
- [/sdlc:commit](./commit.md): commits the changes after a run.
- [/sdlc:review](./review.md): reviews the changes after a run.
- [/sdlc:pr](./pr.md): opens the pull request after a run.
