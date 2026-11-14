# Conclude, stop and recover a run

Read this page when the skill asks "Is this all? Conclude the plan?", when you want to stop a plan run, or when a context compaction interrupts a run.

The question has three options:

- **Conclude (Recommended)**: finish the plan as it is now.
- **Change more**: give one more change. The skill applies it and asks again.
- **Stop**: end the run here.

The table below gives the exact effect of each option at each point.

## Conclude question

The skill asks "Is this all? Conclude the plan?" at each point where a run
can end open. The options are **Conclude (Recommended)**, **Change more** and
**Stop**. A free-text answer counts as one more change: the skill applies it
and asks again. Each point changes only what the options do:

| Point | Trigger | Conclude | Change more | Stop |
|---|---|---|---|---|
| Before run | a user message before the first `plan_prepare` call | no question. Add the message to the request | — | — |
| Mid-run | a user message after the first `plan_prepare` call (Steps 0 to 7), before the done marker, that answers no skill question | continue at `checkpoint.step`. After a material change at Step 6.5, 6.6 or 7, continue at Step 5 with the next round. Run every step that is left. Step 7 writes done | wait for the next message. Apply it. Ask again | the conclude stop rule. Print the plan path and the step. End |
| Gate | Step 6.5 `results[]` has a status that is not `ok`, Step 6.6 `findings` is not empty, or the gate tool returns an error, and no user message waits | fix each finding (a tool error has no fix). Run the same gate again. Add one try to `gate-tries`. With findings or an error after 3 tries: ask again with no Conclude option | apply the change. Set `gate-tries` to 0. Run Step 6.5 again | the conclude stop rule. Print the findings. End |
| After handoff | a reply to the Step 7 handoff that asks for a change | the **Rejected handoff with feedback** rule of Step 7 | wait, apply, ask again | end. The plan file stays. No state call |

With `--auto`, or in a session with no AskUserQuestion, the skill takes
Conclude at every conclude question. The `--auto` row of the
[flags](./flags-and-gotchas.md#flags) table has the full rule.

### Gate tries

`gate-tries` is the `main` evidence item `gate-tries` with the body
`{"step":"6.6","tries":2}`. `step` is the gate, `6.5` or `6.6`. The count
starts at 0. It goes back to 0 when the stored `step` is not the gate that
failed. A gate tool error counts as one try. A resume after a compaction
reads the stored count back.

### Stop rule

The stop rule has three steps:

1. It writes `plan_mark({ marker: "stopped" })`. On an error, it prints the
   error and tells you that the run shows as running until the 24-hour
   prune.
2. It stops each Step 1 explorer that still runs. For a failed `TaskStop`,
   or an explorer with no known task ID, it prints a warning and adds the id
   to `notStopped` in `dispatch-status`.
3. It prints the `notStopped` ids, the plan path and the step.

The Step 5 review-limit routes also run the stop rule: a `stop` answer, more
than 200 open findings, and AskUserQuestion not available. When the
`stopped` marker is written, the dashboard shows the run with status
`failed` and the issue `Stopped by the user at <station>`. After the handoff
the run is already done, so a rejected handoff makes no state call.

## Recovery after compaction

A plan run continues after Claude Code compacts the session context. You do
not type a command.

- What you see: `Active plan (post-compact): step <n>, branch <b>; plan file: <path>` in the session context, followed by a line that tells Claude to invoke the sdlc:plan skill first when the skill instructions are not in context, and a `Resume with:` line. The banner shows only for a run that was written in the last 24 hours (`state.PlanRunLiveWindow`).
- What the skill does: reloads the run with `plan_prepare` (resume mode, with the `runId` of the `Resume with:` line), reads the evidence digest, then resumes at the saved step; it does not clear the plan file.
- When the saved run is gone: if the resume call returns `no active plan run`, `plan run <runId> not found`, `plan run <runId> is done`, `plan run <runId> is stopped` or `plan run <runId> has no skillInvoked marker`, the skill prints "No active plan run to resume — starting a new plan.", ignores the `Active plan (post-compact):` line for the rest of the session, and starts a new plan.
- When the run disappears in the middle of a plan: if a call returns `plan run <runId> not found` (or a reuse `plan_prepare` call returns `is done` or `has no skillInvoked marker`), the skill tells you that the run state is gone. If a reuse `plan_prepare` call returns `plan run <runId> is stopped`, the skill tells you that the run is stopped: its state stays on disk until it is pruned, but the run cannot continue. In both cases, it stops the background agents that it still holds, names each agent that it could not stop, and starts a new plan with no `runId`.
- Where evidence lives: `<main-worktree>/.sdlc-v2/runs/<runId>.evidence/`. Ship's `cleanup-pipeline` step deletes the plan run and its evidence only when the ship run links a plan run and the ship report file exists on disk. Otherwise the GC sweep removes them after the TTL. The GC sweep keeps the newest plan state file of a branch that still exists, even past the TTL; `execute_state` gc reaps the evidence directory after the TTL.
- A second plan session on the same branch keeps its own run and its own evidence. A new run of the branch deletes an older run only when that run is not done, was not written in the last 24 hours (`state.PlanRunLiveWindow`), and has no `sessionId` that differs from the writer's.
