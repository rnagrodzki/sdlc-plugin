# Execute-Plan State File Format

The `execute` skill writes a JSON state file to `.sdlc-v2/execution/` at execution start and updates it after each wave and task. This file enables crash recovery via `--resume` and provides a transparent record of every wave and task executed during the run.

JSON Schemas are available at `schemas/execute-state.schema.json` and `schemas/ship-state.schema.json` for validation and IDE autocompletion.

---

## File Location

```
<main-worktree>/.sdlc-v2/execution/execute-<branch>-<timestamp>.json
```

- `<main-worktree>` — absolute path to the main git working tree (see [Worktree Safety](#worktree-safety) below)
- `<branch>` — current git branch name with `/` replaced by `-`
- `<timestamp>` — ISO 8601 UTC timestamp at execution start, compacted to `YYYYMMDDTHHmmssZ`

Example: `.sdlc-v2/execution/execute-feat-my-feature-20260328T143000Z.json`

---

## Worktree Safety

State files are always written to the **main working tree's** `.sdlc-v2/execution/`, not the current working directory. This ensures state survives worktree cleanup — if `execute` runs inside a linked worktree, the state file is still accessible after that worktree is removed.

**Main working tree resolution:**

Run the following command and take the path from the first `worktree <path>` line:

```bash
git worktree list --porcelain
```

Example output:

```
worktree /Users/dev/myrepo
HEAD abc123def456
branch refs/heads/main

worktree /Users/dev/myrepo/.worktrees/feat-my-feature
HEAD 789abc012def
branch refs/heads/feat/my-feature
```

The main working tree is `/Users/dev/myrepo`. The state file is written to `/Users/dev/myrepo/.sdlc-v2/execution/`.

If there is only one worktree entry (no linked worktrees), the main working tree is the current repo root.

---

## Top-Level Schema

```json
{
  "version": 1,
  "skill": "execute",
  "startedAt": "2026-03-28T14:30:00Z",
  "branch": "feat/my-feature",
  "planPath": "/Users/dev/myrepo/tasks/plan.md",
  "planHash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2",
  "preset": "balanced",
  "totalTasks": 8,
  "plannedTaskIds": ["1", "2", "3", "4", "5", "6", "7", "8"],
  "waves": [ ... ],
  "context": { ... },
  "issues": [ ... ]
}
```

| Field        | Type          | Description                                                                          |
|--------------|---------------|--------------------------------------------------------------------------------------|
| `version`    | number        | Schema version. Always `1` for the current format.                                   |
| `skill`      | string        | Always `"execute"`. Disambiguates from `ship` state files in the same directory. |
| `startedAt`  | string        | ISO 8601 UTC timestamp when execution was invoked.                                   |
| `branch`     | string        | Git branch name at execution start.                                                  |
| `planPath`   | string \| null | Absolute path to the plan file, resolved once in Step 1 (LOAD) from `--plan <path>` or the positional plan-file-path argument (implements R40). Populated on every run — the standalone plan-argument gate (R41) and ship's own plan-file validation both require an explicit plan file before execution starts, so this field is no longer left unconditionally `null`. `null` only appears in state files written before #505. |
| `planHash`   | string        | SHA-256 hash of the plan file's bytes at execution start, computed by the skill via `shasum -a 256` (implements R40) and passed straight through to the `execute_state({action:"init"})` tool call — the Go tool does not compute or validate it. Populated whenever `planPath` is populated, per the schema (`schemas/execute-state.schema.json`), which types this field as a required non-null string. |
| `preset`     | string \| null | Execution preset (`"full"`, `"balanced"`, or `"minimal"`), or `null` if none was applied. Legacy `"A"`/`"B"`/`"C"` values may appear in older state files. |
| `totalTasks` | number        | Total number of tasks across all waves.                                              |
| `plannedTaskIds` | string[] \| absent | Every task ID the plan declares, passed as an optional `plannedTaskIds` array on `init`. Absent on state files from a run that didn't supply it (including all pre-this-field state files). Two consumers depend on it: `execRunInFlight` (an ID here with no matching entry in `context.completedTaskIds` means the run is still in flight, triggering `resumeBriefing` on `read`) and `verify-completeness` (cross-checks this list against what actually got recorded; without it, `verify-completeness` fails outright with `"verify-completeness cannot find plannedTaskIds in state — invariant check cannot run"` rather than silently skipping the check). |
| `waves`      | array         | Ordered list of wave records (see below).                                            |
| `worktree`   | string \| absent | Absolute realpath of the active worktree at init. Optional, absent on pre-#501 state files. Display/diagnostic metadata only—does not affect state file location keying or resume behavior. Used to scope the session-start banner to the active worktree. |
| `context`    | object        | Accumulated cross-wave context enabling fresh-session resume (see below).            |
| `issues`     | array \| absent | Accumulated non-fatal problems recorded during the run — guardrail violations, wave failures, spec-compliance findings, and similar. See [`issues` Array](#issues-array) below. Absent until the first issue is appended. |
| `runStatus`  | string \| absent | `"completed"` once `cleanup` has stamped the run terminal. Absent for a run still in progress or never explicitly cleaned up. See [Cleanup](#cleanup) below — `cleanup` no longer deletes the state file, it stamps this field instead. |
| `runCompletedAt` | string \| absent | ISO 8601 UTC timestamp written alongside `runStatus:"completed"` by `cleanup`. Absent under the same conditions as `runStatus`. |

---

## `waves` Array

Each element represents one execution wave in order. Wave `0` is the pre-wave (sequential setup tasks); subsequent waves are parallel execution groups.

```json
[
  {
    "number": 0,
    "status": "completed",
    "startedAt": "2026-03-28T14:30:05Z",
    "completedAt": "2026-03-28T14:31:00Z",
    "tasks": [ ... ]
  },
  {
    "number": 1,
    "status": "in_progress",
    "startedAt": "2026-03-28T14:31:05Z",
    "tasks": [ ... ]
  },
  {
    "number": 2,
    "status": "pending",
    "tasks": [ ... ]
  }
]
```

| Field         | Type    | Present when                             | Description                                                          |
|---------------|---------|------------------------------------------|----------------------------------------------------------------------|
| `number`      | number  | always                                   | Wave number. `0` for the pre-wave; `1`, `2`, ... for execution waves.|
| `status`      | string  | always                                   | Current wave status (see [Status Values](#status-values) below).     |
| `startedAt`   | string  | status is `in_progress` or later         | ISO 8601 UTC timestamp when the wave began. Set once on the first `wave-start` and never overwritten on resume — a wave re-entered via `--resume` keeps its original `startedAt`, so wall-clock deadline enforcement (`executeWaveTimeout`) measures from the true start, not the resume time. |
| `completedAt` | string  | status is `completed`, `partial`, or `failed` | ISO 8601 UTC timestamp when the wave finished — successfully, partially (timed out), or with an error. |
| `timedOut`    | boolean | present only on a wave whose deadline elapsed | `true` when `execute_state({action:"wave-done", wave:<n>, status:"partial", timedOut:true})` recorded that the wave's wall-clock deadline (`executeWaveTimeout`) elapsed before every in-wave task finished. Written only by that call — main context reads this field but never writes it. Absent on waves that never timed out. |
| `committedSha` | string \| absent | present only after a commit was recorded for this wave | The git commit SHA covering this wave's changes. Written by `wave-commit` or the legacy `wave-committed` (see below). Absent until the wave has been committed. |
| `tasks`       | array   | always                                   | Per-task records for this wave (see below).                          |

### Recording a wave's commit: `wave-commit` vs `wave-committed`

Two actions can set `committedSha` on a wave row. Both require the wave's `status` to already be
`"completed"` (call `wave-done` first) — neither will commit or record against a wave that is
still `pending`, `in_progress`, or `failed`.

- **`wave-commit`** (current path, see `execute/SKILL.md`'s `## Commits` section) — the tool does
  the committing. Given `wave` and a caller-authored `message`, it stages (`git add -A`), and if
  there is a staged diff, commits it (`git commit -m <message>`, message used verbatim) and writes
  the resulting SHA to `committedSha`. An empty diff is a soft success —
  `{committed:false, reason:"nothing to commit"}` — not an error. If `execute.commitWaves` is
  `false` in config (default `true`), it commits nothing and returns an instruction to commit
  manually and then call `wave-committed` with the resulting SHA. Idempotent on resume: if
  `committedSha` is already set and still a git ancestor of HEAD, it returns
  `{committed:true, sha:<existing>, idempotent:true}` without committing again; if the existing
  SHA is *not* an ancestor (branch was reset/rebased since), it hard-refuses rather than silently
  committing over history that moved.
- **`wave-committed`** (legacy fallback) — records a caller-supplied `sha` directly; it never runs
  git itself. This is the action `wave-commit` itself points callers to when
  `execute.commitWaves` is `false`. Idempotent only for an exact repeat of the same `sha`
  (`{committedSha:<sha>, idempotent:true}`); recording a *different* SHA over an existing one is a
  hard error (`"wave %d already has committedSha %q — refusing to overwrite with %v"`), not a
  silent overwrite.

---

## `waves[].tasks` Array

Each element represents one task within its wave.

```json
[
  {
    "id": 1,
    "name": "Set up database schema",
    "complexity": "Standard",
    "risk": "Low",
    "status": "completed",
    "filesChanged": ["db/schema.sql", "db/migrations/001_init.sql"],
    "completedAt": "2026-03-28T14:30:58Z"
  },
  {
    "id": 2,
    "name": "Implement auth middleware",
    "complexity": "Complex",
    "risk": "Medium",
    "status": "failed",
    "filesChanged": [],
    "completedAt": "2026-03-28T14:31:40Z"
  }
]
```

| Field          | Type     | Description                                                                          |
|----------------|----------|--------------------------------------------------------------------------------------|
| `id`           | number   | Task number from the plan. Stable across resume attempts.                            |
| `name`         | string   | Task title as written in the plan.                                                   |
| `complexity`   | string   | Task complexity classification: `"Trivial"`, `"Standard"`, or `"Complex"`.          |
| `risk`         | string   | Task risk classification: `"Low"`, `"Medium"`, or `"High"`.                         |
| `status`       | string   | Task outcome: `"completed"`, `"failed"`, or `"skipped"`.                            |
| `filesChanged` | string[] | Repository-relative paths of files this task modified, derived from `git diff` after task completion. Empty array if the task produced no file changes. |
| `completedAt`  | string   | ISO 8601 UTC timestamp when `task-done`/`task-fail` recorded this task's outcome. Task rows never carry a `startedAt`: `task-done`/`task-fail` run after the task has already finished, so there is no truthful start time to record. Per-task liveness while a task is still in flight comes from the progress marker instead (see [Progress Markers](#progress-markers-in-flight-wave-liveness) below), not from a field on this row. |

---

## `context` Object

Accumulates cross-wave state so that a resumed execution in a fresh Claude session has sufficient context to continue without re-reading completed work. Updated after each wave completes.

```json
{
  "planSummary": "Add OAuth2 login flow with JWT token issuance",
  "completedTaskIds": [1, 3, 4],
  "filesAdded": ["src/auth/oauth.ts", "src/auth/jwt.ts"],
  "filesModified": ["src/routes/index.ts", "src/middleware/session.ts"],
  "interfacesCreated": ["OAuthProvider", "JWTClaims", "TokenIssuer"],
  "decisionsFromPriorWaves": [
    "Used HS256 for JWT signing; RS256 deferred to follow-up",
    "OAuth state parameter stored in Redis, not session cookie"
  ]
}
```

| Field                    | Type     | Description                                                                                                   |
|--------------------------|----------|---------------------------------------------------------------------------------------------------------------|
| `planSummary`            | string   | One-line description of the overall plan goal. Injected into agent prompts at resume to orient the session.  |
| `completedTaskIds`       | number[] | Task IDs that completed successfully across all waves so far. Used to skip already-done work on resume.      |
| `filesAdded`             | string[] | All files created by completed waves. Helps resuming agents understand what already exists.                  |
| `filesModified`          | string[] | All pre-existing files modified by completed waves. Alerts resuming agents to changed interfaces.            |
| `interfacesCreated`      | string[] | Key interfaces, types, and exports introduced in completed waves. Enables resuming agents to use them correctly without re-reading source files. |
| `decisionsFromPriorWaves`| string[] | Implementation decisions made in earlier waves that affect how remaining waves should be implemented. Prevents contradictory choices in resumed sessions. |

---

## `issues` Array

A flat, run-wide log of non-fatal problems, appended to (never rewritten) as the run progresses —
guardrail violations, wave failures, spec-compliance findings, and similar. Not scoped under
`context` or a specific wave row; it is its own top-level array so it can be read and summarized
independently of wave/task status.

```json
{
  "issues": [
    {
      "wave": 2,
      "severity": "error",
      "category": "wave-fail",
      "summary": "Wave 2 failed",
      "detail": "timed out",
      "timestamp": "2026-03-28T14:35:00Z"
    }
  ]
}
```

| Field       | Type   | Description                                                                       |
|-------------|--------|-------------------------------------------------------------------------------------|
| `wave`      | number \| absent | Wave number the issue relates to, when applicable.                     |
| `step`      | string \| absent | Skill step the issue was raised from (e.g. a guardrail-check step name), when applicable. |
| `taskId`    | string \| absent | Task ID the issue relates to, when applicable.                         |
| `severity`  | string | Free-text severity, e.g. `"error"`, `"warning"`.                                    |
| `category`  | string | Free-text category, e.g. `"wave-fail"`, `"guardrail"`, `"spec-compliance"`.          |
| `summary`   | string | One-line description.                                                               |
| `detail`    | string \| absent | Longer explanation, when available.                                     |
| `timestamp` | string | ISO 8601 UTC timestamp when the issue was appended.                                 |

Issues accumulate via `execAppendIssue` — several actions append to this array as a side effect of
their normal work (for example, `wave-fail` appends a `category:"wave-fail"` issue automatically);
there is no dedicated "add issue" action for the skill to call directly. `wave-start`, `wave-done`,
and `wave-fail` each return a rolling `issueCount`/`issueHighlights` summary of this array in their
narration so the skill doesn't need to read the full array to report on it mid-run — see
`execute/SKILL.md`'s `## Wave loop` section.

A fuller grouped summary (counts by category, plus a suggested hardening action) is planned as an
`issueSummary`/`hardenSuggestion` shape but has not landed in `execute_state.go` as of this
writing — `execute/SKILL.md`'s Step 9 report references the shape it is expected to take once it
does; treat that reference as forward-looking, not as documentation of an action that exists today.

---

## Status Values

| Status        | Meaning                                                                          |
|---------------|----------------------------------------------------------------------------------|
| `pending`     | Not yet started; waiting for preceding waves to complete.                        |
| `in_progress` | Currently executing. If the process crashes, this wave or task will be retried.  |
| `completed`   | Finished successfully.                                                           |
| `failed`      | Terminated with an error; execution halted.                                      |
| `partial`     | Wave only. The wave's `executeWaveTimeout` deadline elapsed before every task finished — some tasks may have completed, others were left unaccounted and reported as errored via `task-fail`. Recorded via `execute_state({action:"wave-done", wave:<n>, status:"partial", timedOut:true})`, which also sets `timedOut: true` on the wave row. Not a crash: execution proceeds to recovery rather than halting. |
| `skipped`     | Intentionally bypassed (e.g. task already completed in a prior attempt).         |

---

## Progress Markers (In-Flight Wave Liveness)

While a wave is running, each dispatched worker records its current phase in a separate marker file — the only signal produced *during* execution. Every field described above (`startedAt`, `completedAt`, `status`, ...) is written after a task or wave finishes; task rows in particular never carry a `startedAt` (see `waves[].tasks[]` above), so this marker is the sole source of per-task liveness while work is still in flight.

**Location:**

```
<stateDir>/<runId>/progress/<taskId>.json
```

- `<stateDir>` — `resolveStateDir()`'s return value, which already ends in `.sdlc-v2/execution`. There is no additional `execution/` path segment: the `progress/` directory sits directly inside the per-run directory, alongside that run's per-task fact sheets (`task-<id>.md`).
- `<runId>` — the run identifier passed to `execute_state({action:"wave-start", runId:...})` and threaded through the wave manifest.
- `<taskId>` — one file per task, written only by that task. Distinct tasks touch distinct files, so two workers updating different tasks at the same time never race on the same file — no read-modify-write step, no locking.

One `progress/` directory covers the entire run, not one per wave — task IDs from every wave land side by side in the same directory, and the wave number plays no part in the path or the write/read call.

Example: `.sdlc-v2/execution/run-20260328T143000Z/progress/3.json`

**Per-task file shape:**

```json
{ "phase": "editing", "updatedAt": "2026-03-28T14:32:10Z" }
```

| Field       | Type   | Description                                                                                    |
|-------------|--------|--------------------------------------------------------------------------------------------------|
| `phase`     | string | One of `started`, `reading`, `editing`, `verifying`, `reporting` — a free-text phase is rejected. |
| `updatedAt` | string | ISO 8601 UTC timestamp of the most recent write for that task.                                   |

Written via `execute_state({action:"wave-progress", runId:<id>, taskId:<id>, phase:<phase>})` — a pure atomic tmp-write + rename of `progress/<taskId>.json`, no read step. The action takes flat top-level fields, not a nested `payload` object, and does not accept a wave number at all.

**Read shape (unchanged):** `execute_state({action:"wave-progress", runId:<id>, readProgress:true})` still returns one aggregated object, keyed by task ID across the whole run:

```json
{
  "tasks": {
    "3": { "phase": "editing", "updatedAt": "2026-03-28T14:32:10Z" },
    "4": { "phase": "verifying", "updatedAt": "2026-03-28T14:32:40Z" }
  }
}
```

It builds this by reading every file in `progress/` and merging in the legacy single-file marker (`<stateDir>/<runId>/progress.json`, from before this per-task-file layout) at lower priority — if a task ID appears in both, the per-task file wins. Nothing writes the legacy path anymore; it is read-only, for backward compatibility with runs that started before this format changed. Missing directory, missing legacy file, or one corrupt per-task file are all swallowed — `readProgress:true` returns `{"tasks":{}}` when no marker exists yet rather than erroring, same as before.

The per-run directory is not swept by the top-level state-file GC (which only scans `.json` files directly under `.sdlc-v2/execution/`) — the whole run directory, `progress/` included, is reaped by `--gc` alongside the fact sheets in that same directory.

---

## Lifecycle Rules

### Cleanup

`execute_state({action:"cleanup"})` does **not** delete the state file. It stamps the branch's
state terminal — `runStatus:"completed"` and `runCompletedAt` (ISO 8601 UTC) — via a normal
`state.Write`, and the file (including its `issues[]`) stays on disk and findable via
`state.Find`, so later reads (for example `/harden` after the run) can still see what happened.
It is `gc`'s TTL sweep, not `cleanup`, that eventually removes the file itself.

What `cleanup` *does* remove — separately from the state file — is the per-run **working**
directory (fact sheets, progress markers) and the ledger directory, since those hold only
working artifacts that are safe to delete once the run is done. It derives the run ID from the
state's `startedAt` field to find them; if `startedAt` is empty or absent, directory removal is
skipped entirely rather than guessed at (an indeterminate run ID must never reach a recursive
delete). The response reports `runDirCleaned`/`ledgerDirCleaned` (booleans) and, on a removal
error, `runDirError`/`ledgerDirError`.

If execution fails or is interrupted, `cleanup` is not called at all — the state file (still
carrying no `runStatus`) is retained so the run can be resumed.

### Resume

Passing `--resume` to `execute` causes it to locate the most recent state file for the current branch (matched by branch name in the filename). The skill does not hand-derive what changed since the last run from `waves[]`/`context` prose — `execute_state({action:"read"})` attaches a `resumeBriefing` (see below) whenever the run is still in flight, and that briefing is the starting point. From there:

1. Skips any wave with status `completed`.
2. Retries any wave with status `in_progress` from its beginning (individual task results within that wave are not trusted — `execute_state({action:"resume-reset"})` clears those rows at resume time, before the resume pointer is computed).
3. Executes remaining waves with status `pending` normally.

The `context` object is loaded into the agent prompt so the resuming session understands what was built in prior waves.

If multiple state files exist for the same branch (from multiple failed attempts), the one with the most recent timestamp is used.

---

## Derived Response Shapes (Not Persisted)

Two shapes ride alongside the state blob on certain actions' responses. Neither is a field written
into the JSON file on disk — both are computed fresh from the persisted fields above every time
they're returned, so there is nothing to keep in sync by hand.

### `resumeBriefing`

Attached under a `resumeBriefing` key on `read` and `resume-reset` responses, but only when
`execRunInFlight()` is true — i.e. some recorded wave isn't `"completed"`, or `plannedTaskIds` has
IDs not yet in `context.completedTaskIds`. A finished, cleaned-up run (or one that never set
`plannedTaskIds`) carries no `resumeBriefing` at all.

```json
{
  "resumeBriefing": {
    "resumable": true,
    "wavesDone": 3,
    "wavesRemaining": 2,
    "gitCrossCheck": "mismatch",
    "gitMismatches": ["wave 2: committedSha a1b2c3d is not an ancestor of HEAD"],
    "willRedo": ["4"],
    "willSkip": ["1", "2", "3"],
    "summary": "...",
    "display": "..."
  }
}
```

| Field            | Type     | Description |
|------------------|----------|--------------|
| `resumable`      | boolean  | Whether resume-reset can proceed automatically. |
| `wavesDone`      | number   | Count of waves recorded with status `"completed"`. |
| `wavesRemaining` | number   | Count of waves recorded but **not yet** `"completed"`. This is *not* the true total wave count from the plan — state doesn't carry that, and re-deriving it would mean re-parsing the plan file at a path that may no longer be valid. Do not present this as "waves left in the plan." |
| `gitCrossCheck`  | string \| absent | One of `"confirmed"` (every recorded `committedSha` checks out as a git ancestor of HEAD) or `"mismatch"` (at least one does not, or its ancestor check itself errored). Absent only when no wave has a `committedSha` yet (nothing to cross-check) — it is present, as `"confirmed"`, on a perfectly healthy resumable run, not just on a problem one. Always soft-reported here — never a hard `read` failure. |
| `gitMismatches`  | string[] \| absent | One entry per wave whose `committedSha` failed the ancestor check, present only when `gitCrossCheck` is `"mismatch"`. |
| `willRedo`       | string[] | Task IDs `resume-reset` would clear and re-run. |
| `willSkip`       | string[] | Task IDs already recorded complete that `resume-reset` would leave alone. |

`resumeBriefing` also embeds `pipeline.Narration` (`summary`, `display`, and optionally `timing`/
`next`) — same envelope shape used across every other `execute_state` action. See
`recovering-from-failures.md` for how this briefing anchors the resume flow, and for the
stalled-vs-timeout distinction (a separate, unrelated signal — do not conflate `gitCrossCheck` with
a stalled or timed-out task).

### `task-context`

Not attached to `read` — its own dedicated action, called by each dispatched worker for itself
(`execute_state({action:"task-context", taskId:"<id>"})`), not by the orchestrating session. It
consolidates everything that worker needs into one response rather than requiring the skill to
inline a fact sheet, guardrails block, and reporting instructions into the dispatch prompt:

| Field        | Type    | Description |
|--------------|---------|--------------|
| `taskId`     | string  | Echoes the requested task ID. |
| `factSheet`  | string  | The task's fact-sheet markdown (`<stateDir>/<runId>/task-<id>.md` — see [Progress Markers](#progress-markers-in-flight-wave-liveness) above for why `<stateDir>` already ends in `.sdlc-v2/execution` with no extra `execution/` segment) — Contract, Acceptance Criteria, Files. |
| `priorWaves` | string  | A live prior-wave summary, re-rendered at call time (not a snapshot from `wave-start`), so it reflects sibling tasks in the same wave that finished since the wave began. |
| `verify`     | string  | Verify-step guidance for this task. |
| `reportBack` | string  | Instructions for how the worker reports its outcome — including that the worker does not call `task-done`/`task-fail` itself; it reports a status block back to the dispatching session, which records the outcome. |
| `truncated`  | boolean \| absent | `true` when the combined payload exceeded the 1 MiB cap and `factSheet` and/or `priorWaves` were shortened to fit. Absent (not `false`) when nothing was truncated. |

See `classifying-and-waving-tasks.md`'s `## Worker dispatch prompt` for the exact two-line prompt
that tells a dispatched worker to call this action.

### A note on `sideEffects`

`ship_state.go` tracks a `sideEffects` list (for actions like PR creation or tag pushes that must
not be silently repeated on ship resume). Execute state has no equivalent concept and this document
deliberately does not add one: execute's waves and tasks aren't the kind of external, non-idempotent
operation `sideEffects` exists to guard — the closest analogue, a wave's git commit, is already
covered by `committedSha`'s own idempotency handling (see above), not by a generic side-effect log.

---

## Full Example

Mid-execution state: wave 0 completed, wave 1 in progress, wave 2 pending.

```json
{
  "version": 1,
  "skill": "execute",
  "startedAt": "2026-03-28T14:30:00Z",
  "branch": "feat/my-feature",
  "planPath": "tasks/plan.md",
  "planHash": "sha256:3f2a1b9c7e4d8a5f6b0c2d9e1a4f7b3c8d2e5f0a1b6c9d4e7f2a5b8c3d6e9f0",
  "preset": "balanced",
  "totalTasks": 8,
  "waves": [
    {
      "number": 0,
      "status": "completed",
      "startedAt": "2026-03-28T14:30:05Z",
      "completedAt": "2026-03-28T14:31:00Z",
      "tasks": [
        {
          "id": 1,
          "name": "Create database schema",
          "complexity": "Standard",
          "risk": "Low",
          "status": "completed",
          "filesChanged": ["db/schema.sql", "db/migrations/001_init.sql"],
          "completedAt": "2026-03-28T14:30:40Z"
        },
        {
          "id": 2,
          "name": "Configure environment variables",
          "complexity": "Trivial",
          "risk": "Low",
          "status": "completed",
          "filesChanged": [".env.example", "src/config.ts"],
          "completedAt": "2026-03-28T14:30:58Z"
        }
      ]
    },
    {
      "number": 1,
      "status": "in_progress",
      "startedAt": "2026-03-28T14:31:05Z",
      "tasks": [
        {
          "id": 3,
          "name": "Implement OAuth2 provider integration",
          "complexity": "Complex",
          "risk": "Medium",
          "status": "completed",
          "filesChanged": ["src/auth/oauth.ts", "src/auth/providers/github.ts"],
          "completedAt": "2026-03-28T14:31:50Z"
        },
        {
          "id": 4,
          "name": "Implement JWT token issuance",
          "complexity": "Standard",
          "risk": "Medium",
          "status": "in_progress",
          "filesChanged": []
        },
        {
          "id": 5,
          "name": "Add user session storage",
          "complexity": "Standard",
          "risk": "Low",
          "status": "pending",
          "filesChanged": []
        }
      ]
    },
    {
      "number": 2,
      "status": "pending",
      "tasks": [
        {
          "id": 6,
          "name": "Wire auth middleware into route handlers",
          "complexity": "Standard",
          "risk": "Low",
          "status": "pending",
          "filesChanged": []
        },
        {
          "id": 7,
          "name": "Add integration tests for login flow",
          "complexity": "Standard",
          "risk": "Low",
          "status": "pending",
          "filesChanged": []
        },
        {
          "id": 8,
          "name": "Update API documentation",
          "complexity": "Trivial",
          "risk": "Low",
          "status": "pending",
          "filesChanged": []
        }
      ]
    }
  ],
  "context": {
    "planSummary": "Add OAuth2 login flow with JWT token issuance and session management",
    "completedTaskIds": [1, 2, 3],
    "filesAdded": [
      "db/schema.sql",
      "db/migrations/001_init.sql",
      "src/auth/oauth.ts",
      "src/auth/providers/github.ts"
    ],
    "filesModified": [
      ".env.example",
      "src/config.ts"
    ],
    "interfacesCreated": [
      "OAuthProvider",
      "OAuthCallbackParams",
      "GitHubOAuthProvider"
    ],
    "decisionsFromPriorWaves": [
      "OAuth state parameter stored in Redis with 10-minute TTL, not in session cookie",
      "GitHub provider implemented first; Google provider deferred to follow-up task"
    ]
  }
}
```
