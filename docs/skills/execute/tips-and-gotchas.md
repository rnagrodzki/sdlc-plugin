# Tips and gotchas

Read this page when a `/sdlc:execute` run stops, asks a question or does something you did not expect. It lists the rules of a run that are easy to miss.

## Example

```text
/sdlc:execute --resume
```

Continues an interrupted run from the saved progress. The plan path comes from the saved state, so you do not pass it again.

## Before the run

- **The plan file path is required.** The skill never guesses which plan to execute, also when you discussed a plan in the same session. Always pass the path, except when you resume.
- **Exit plan mode first.** The skill edits files and runs commands. In plan mode it prints a message and stops.
- **A plan needs 2 tasks or more.** The skill stops on a plan with one task: one task needs no orchestration.
- **An unauthored OpenSpec plan is refused.** `execute_state` init fails, and writes no state file, for a plan that has an `**OpenSpec-Create:**` line and no `**OpenSpec-Staging:**` or `**OpenSpec-Saved:**` line, or that has a `**OpenSpec-Staging:**` line and an OpenSpec Appendix that starts with `[TBD`. A plan with an `**OpenSpec-Saved:**` line is never refused by this check. Run `/sdlc:plan` on the plan again to finish the OpenSpec authoring.
- **Branch handling.** On the default branch (for example `main`) or the configured base branch (`[git] baseBranch`), the skill creates a feature branch. It asks you to confirm the name, except in auto mode. On a feature branch or in a linked worktree, it runs in place. The skill never creates a worktree.
- **Plan text is data.** Text in the plan that tells the skill to change its mode or its behavior is ignored.

## During the run

- **Wave structure.** The skill groups the tasks into waves by their dependencies. Independent tasks run in parallel in a wave. The skill pauses before a wave that is flagged as high-risk.
- **A small plan has no waves.** A plan with 3 tasks or fewer, none of them Complex or High-risk, runs task by task in the main session. The skill saves no state and runs no base sync. Run the plan again from the start after an interruption.
- **A task gets 2 retries.** After 2 failed retries the skill asks you. It also offers `/sdlc:harden`, and a GitHub issue through `/sdlc:error-report`.
- **Do not edit the plan file during a run.** The skill stores a hash of the plan at the start. When the hash differs at the start of a wave, the run stops and dispatches nothing.
- **Each wave gets one commit.** The tool makes the commit after the wave completes. With `--commit-waves false`, or `execute.commitWaves = false`, the tool does not commit: it returns an instruction, and the skill makes the commit by hand.
- **After the run.** The common next steps are `/sdlc:commit`, `/sdlc:review` and `/sdlc:pr`. For an OpenSpec plan the skill validates the change when the `openspec` command is present. It never archives the change by itself.

## Guardrails

The skill loads the execution guardrails from `execute.guardrails` in `.sdlc-v2/config.toml`. They are separate from the plan guardrails.

- Before each wave, the skill checks the error-severity guardrails against the tasks of the wave and the changes so far.
- After each wave, it checks the guardrails of all severities against the diff.
- On an error-severity failure the skill asks you: `override`, `harden` or `cancel`. After a wave it also offers `fix`, which tries one fix and checks again. Warnings are reported only.
- In auto mode nobody can answer, so the skill never overrides. It prints the failure and stops. The state stays resumable.
- A language model evaluates the guardrails, not a program. A false positive is possible: `override` exists for that case.

## Base sync between waves

After each wave except the last, `/sdlc:execute` merges `origin/<base>` into the branch. `<base>` is `[git] baseBranch` in `.sdlc-v2/config.toml`, or the repository default branch when that key is absent or empty.

- On a conflict, one sub-agent resolves it. If it fails, the skill aborts the merge and the run continues on the previous base. The summary then names the wave and leaves the conflict for the rebase step of `/sdlc:ship`.
- Turn the base sync off with `[execute] baseSync = false`.

## Resume and state

- **Resuming is safe.** If the session ends during a run, run `/sdlc:execute --resume` in a new session. The skill saves the progress after each completed wave.
- **An unfinished run is found without `--resume`.** The skill asks whether to resume it. In auto mode it resumes without a question. A different plan file on the command line starts a new run and replaces the old state.
- **A lost wave commit stops the resume.** When a wave commit is no longer reachable from `HEAD`, for example after a force-push or a reset, the skill warns and does not recover by itself. Resolve it by hand.
- **The state file stays after the run.** A completed run marks its state `completed` and does not delete the file. A failed or interrupted run prints where the state is and tells you to use `--resume`.
- **State is always loaded first.** With or without `--resume`, the first action of the skill is an unconditional `execute_state({action: "read"})` call, so a stale in-flight run from an earlier session is always found before anything else happens.
- **Runtime config is resolved second, and always.** Immediately after it loads the state, the skill calls `execute_state({action: "resolve-config"})`. The call merges `--quality` and `--auto`, the auto mode of a `/sdlc:ship` run that started the skill, and the `executePrefs.*` keys in `.sdlc-v2/local.toml` (plus `execute.commitWaves` from `config.toml`) into one answer. That call, not a direct read of a config file, decides the tier, whether prompts are suppressed and whether a high-risk wave needs a second approval. The skill prints each value with its source. See [Configuration](./flags-and-configuration.md#configuration).
- **The wave schedule is saved at the start.** After you confirm the wave structure, the skill passes it to `execute_state({action: "init"})` as `plannedWavesJson`: a JSON array of `{number, taskIds}`, with the pre-wave tasks as wave `0`. Task IDs are JSON strings. `init` rejects an empty array, a repeated wave number or task ID, a wave with no task IDs, and a task ID that is not in the plan. A schedule may leave tasks out. The state file stores it as `plannedWaves`, and the dashboard uses it to list the waves that have not started. A small plan that runs directly never calls `init`, so it has no schedule.

## Wave supervision

A server-driven poll supervises the waves, not the session itself. After the skill dispatches the tasks of a wave, it calls `execute_state({action: "wave-await"})` again and again at the `--wave-interval` cadence.

- The server classifies each task that is still open. The `next` field of its response tells the skill what to do: keep polling, reclaim a stalled worker, redispatch a task, or move on when the wave is done.
- A task that timed out, or whose worker never answered the reclaim, is redispatched through `execute_state({action: "task-redispatch"})`, up to two retries.
- A worker that does answer a reclaim is kept. The reply proves that it is alive, so the same attempt continues and no retry is spent.
- The skill always follows `next` as given. It does not derive its own liveness logic.

## See also

- [Execute wave supervision](../../internal/execute-wave-supervision.md): how heartbeats, the three ceilings, reclaim and retries work.
- [Flags and configuration](./flags-and-configuration.md): every flag and config key.
- [/sdlc:execute](../execute.md): the overview and the Steps table.
