# Execute Wave Supervision

> Verified against: sdlc v0.1.4

This document describes how the `/execute` skill supervises a running wave:
what counts as a heartbeat, the ceilings that turn silence into a failure,
what a reclaim actually does, and how retries carry partial work forward. For
the user-facing skill definition, see
[`plugins/sdlc/skills/execute/SKILL.md`](../plugins/sdlc/skills/execute/SKILL.md).
Everything here is server-driven: the calling skill never computes elapsed
time or classifies a stall itself — it calls `execute_state({action:
"wave-await"})` and follows the returned `next` instruction verbatim.

---

## Waves and Workers

A **wave** is a group of tasks dispatched together, in parallel, from the
main orchestrating session. Each task in a wave runs as its own dispatched
Agent (a **worker**) — or, for a batch of trivial tasks, one worker handling
several tasks at once. The orchestrating session never executes wave tasks
itself; it dispatches, waits, and records outcomes.

Once dispatched, a worker is opaque to the orchestrator except for what it
reports back and the liveness signals described below. `wave-await` is the
mechanism that watches every still-open task in a wave and decides, on each
poll, whether to keep waiting, reclaim a quiet worker, or fail one that never
answers.

## Heartbeats

A **heartbeat** is any timestamp update that proves a worker is still doing
work. Two things produce one:

1. **Explicit phase calls.** A worker calls
   `execute_state({action: "wave-progress", runId, taskId, phase: "..."})`
   as it moves through its work (see [The Five Phases](#the-five-phases)
   below). Each call stamps `updatedAt` on that task's progress record.
2. **The automatic `PostToolUse` stamp.** Every successful `Edit` or `Write`
   tool call the worker makes triggers the wave-liveness hook
   (`internal/hooks/wave_liveness.go`). The hook matches the edited file
   against the wave's planned file list; if it uniquely identifies one open
   task, it calls `wave.TouchProgress` to bump that task's `updatedAt` —
   without requiring the worker to make an explicit `wave-progress` call.
   This means a worker that is silently heads-down editing files still
   produces heartbeats, one per successful edit.

The hook fails open on every ambiguous case: no execute state, no `runId`,
no file match, or a file that matches more than one open task all result in
a silent no-op, never a blocked tool call.

`Subject.Liveness` — the timestamp `wave-await` checks against the
heartbeat-staleness threshold — is the more recent of these two sources for
a given task.

## The Five Phases

`wave-progress` accepts exactly five phase values: `started`, `reading`,
`editing`, `verifying`, `reporting`. The set is fixed (a free-text phase is
rejected) because the phase itself carries no supervision logic — only the
timestamp does. Phases exist for readability in progress dumps and to let a
worker record a `lastCompletedTask`/`acceptanceDone`/`filesTouched`
snapshot at a natural checkpoint, not to gate any classification decision.
`wave-await` never inspects *which* phase a task is in, only *when* it was
last updated.

## The Three Ceilings

Three numbers bound how long `wave-await` will wait before treating a task
as failed. All three derive from `--wave-interval` (`waveIntervalSeconds`,
default `60`) and `--wave-timeout` (`waveTimeoutSeconds`, default `1800`),
both recorded once at `init` and read back by `execWaveStallTimeouts`.

| Ceiling | Formula | Default | What it gates |
|---|---|---|---|
| Heartbeat staleness | `10 × waveIntervalSeconds` | `600s` | How long a task can go without a heartbeat before it is considered stalled or never-started. |
| Reclaim grace | `max(5 × waveIntervalSeconds, 300s)` | `300s` | How long a worker has to answer a reclaim `SendMessage` before it is failed. |
| Total timeout | `waveTimeoutSeconds` | `1800s` | The absolute ceiling on a task's dispatch-to-resolution time, regardless of heartbeat freshness. |

The total timeout is unchanged from earlier versions of this mechanism. The
heartbeat-staleness and reclaim-grace thresholds were both widened in the
same change: heartbeat staleness from `3 × waveIntervalSeconds` (180s) to
`10 × waveIntervalSeconds` (600s), and reclaim grace from
`max(2 × waveIntervalSeconds, 120s)` (120s) to
`max(5 × waveIntervalSeconds, 300s)` (300s). The widening reduces false
positives from workers that are legitimately busy (e.g. a long test run or a
long single edit) without an intervening heartbeat.

## Classification Precedence

`wave.ClassifyTask` evaluates a still-open task against the three ceilings
in a strict, first-match-wins order:

1. **Timeout** — dispatched longer than the total timeout. Wins outright,
   even over a reclaim already in flight; there is no reclaim attempt for a
   task this old.
2. **Never-started** — no `task-context` call recorded (the worker never
   even fetched its fact sheet) and dispatched longer than the
   heartbeat-staleness threshold.
3. **Stalled** — has a heartbeat on record, but the most recent one is
   older than the heartbeat-staleness threshold.
4. **None** — otherwise healthy; stays open.

Never-started and stalled both lead to the same next step — a reclaim is
stamped and relayed — but they are distinct verdicts because a task that
never fetched its context is a different failure shape than one that
started and then went quiet.

## Reclaim: A Liveness Check, Not a Kill Order

A **reclaim** fires when `wave-await` classifies a task as never-started or
stalled and no reclaim is already in flight for it. `wave-await` stamps
`reclaimRequestedAt` on the task's server state and instructs the
orchestrating skill to `SendMessage` a liveness-check prompt to the worker,
verbatim. This is a question, not a kill order: it does not stop the
worker, does not touch its retry count, and does not fail the task.

Two things can happen next:

- **The worker answers.** Any heartbeat recorded after `reclaimRequestedAt`
  — a `wave-progress` call or a matched `Edit`/`Write` stamp — counts as an
  answer. `wave-await` clears the reclaim stamp and returns the task to the
  open bucket. The **same attempt continues**: no `WaveAwaitFailure`, no
  `TaskStop`, no retry consumed. The reply itself is the proof of life.
- **The worker never answers.** Once the reclaim grace period elapses with
  no qualifying heartbeat, the task is classified `STALLED_NO_REPLY` and
  handled exactly like a timeout: `TaskStop`, `task-fail`, and (retries
  permitting) `task-redispatch`.

There used to be a third outcome — `STALLED_RECLAIMED`, for a worker that
answered late but still within grace — which counted as a failure and
consumed a retry even though the worker was alive the whole time. That cause
no longer exists. Answering a reclaim, at any point before the grace period
elapses, keeps the attempt going.

## Failure Causes

Exactly two causes can appear in `wave-await`'s `ext.failed[].cause`:

| Cause | Meaning | Retry budget |
|---|---|---|
| `TIMEOUT` | The task has been dispatched longer than the total timeout, regardless of heartbeat freshness. | Counts toward the 3-attempt ceiling. |
| `STALLED_NO_REPLY` | The worker went quiet, was sent a reclaim, and never replied within the reclaim grace period. | Counts toward the 3-attempt ceiling. |

A worker that answers its reclaim never appears in `ext.failed[]` at all —
it was never a failure.

## Retries and `resumeFrom`

The retry ceiling is unchanged: **2 retries, 3 attempts maximum.** Attempt 1
is the initial dispatch; attempts 2 and 3 are retries. `waveAwaitRetriesLeft`
computes `3 - attempt`; once that reaches `0`, the `next` instruction orders
escalation to the user instead of another `task-redispatch`.

`wave-await` no longer carries its own `resumeFrom` field on
`WaveAwaitFailure` — that field was removed along with `STALLED_RECLAIMED`.
Partial-work continuity for a redispatched retry instead comes from
`task-fail`, which harvests `acceptanceDone`, `filesTouched`,
`lastCompletedTask`, and `blocker` from the worker's last `wave-progress`
write and attaches them to the retry context. This applies uniformly to
both remaining causes — a `TIMEOUT` retry and a `STALLED_NO_REPLY` retry
both get whatever partial-work claim the failed worker last recorded.

## Tuning `--wave-interval` and `--wave-timeout`

- **`--wave-interval <s>`** (default `60`) sets the poll cadence between
  `wave-await` calls, and also derives both the heartbeat-staleness
  threshold (`10×`) and the reclaim grace floor (`5×`, minimum `300s`).
  Raising it loosens both thresholds together — useful for slower or
  noisier workers, at the cost of a longer detection lag for a genuinely
  dead worker. Lowering it tightens both thresholds and shortens the poll
  cadence itself.
- **`--wave-timeout <s>`** (default `1800`) sets the absolute per-task
  ceiling, independent of heartbeat freshness. Raise it for waves with
  tasks that legitimately run long (large builds, slow test suites);
  lowering it makes `TIMEOUT` fire sooner regardless of how healthy the
  worker's heartbeat looks.

Both flags are recorded once at `init` as `waveTimeoutSeconds`/
`waveIntervalSeconds` on the execute state file and read back by every
`wave-await` call for that run — changing them mid-run has no effect; they
apply to the next `execute` invocation.

## Known Blind Spot

The automatic `PostToolUse` stamp fires only when a tool call **returns**.
A single `Edit` or `Write` call that itself takes longer than the
heartbeat-staleness threshold (600s by default) — for example, a very large
file rewrite on a slow filesystem — produces no heartbeat while it is in
flight. `wave-await` has no way to distinguish "worker is mid-edit on a slow
call" from "worker went quiet" until that call returns and the hook fires.
In practice this is rare at the default 600s threshold, but it means the
staleness check is not a perfect proxy for worker health — only for
*recorded* activity.

---

## Sources of Truth

| Section | Primary Sources |
|---|---|
| Heartbeats, blind spot | `internal/hooks/wave_liveness.go`, `internal/wave/progress.go` |
| Five phases | `plugins/sdlc/skills/execute/state-format.md` (Progress Markers), `internal/wave/progress.go` |
| Three ceilings, classification precedence | `internal/wave/serverstate.go` (`ClassifyTask`), `internal/tools/execute_wave_await.go` (`execWaveStallTimeouts`) |
| Reclaim, failure causes, retries | `internal/tools/execute_wave_await.go` (`execActionWaveAwait`), `plugins/sdlc/skills/execute/recovering-from-failures.md` |
| Tuning | `plugins/sdlc/skills/execute/SKILL.md` (Step 0), `docs/skills/execute.md` |
