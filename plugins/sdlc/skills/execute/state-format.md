# Execute-Plan State File Format

The `execute` skill writes a JSON state file to `.sdlc/execution/` at execution start and updates it after each wave and task. This file enables crash recovery via `--resume` and provides a transparent record of every wave and task executed during the run.

JSON Schemas are available at `schemas/execute-state.schema.json` and `schemas/ship-state.schema.json` for validation and IDE autocompletion.

---

## File Location

```
<main-worktree>/.sdlc/execution/execute-<branch>-<timestamp>.json
```

- `<main-worktree>` — absolute path to the main git working tree (see [Worktree Safety](#worktree-safety) below)
- `<branch>` — current git branch name with `/` replaced by `-`
- `<timestamp>` — ISO 8601 UTC timestamp at execution start, compacted to `YYYYMMDDTHHmmssZ`

Example: `.sdlc/execution/execute-feat-my-feature-20260328T143000Z.json`

---

## Worktree Safety

State files are always written to the **main working tree's** `.sdlc/execution/`, not the current working directory. This ensures state survives worktree cleanup — if `execute` runs inside a linked worktree, the state file is still accessible after that worktree is removed.

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

The main working tree is `/Users/dev/myrepo`. The state file is written to `/Users/dev/myrepo/.sdlc/execution/`.

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
  "waves": [ ... ],
  "context": { ... }
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
| `waves`      | array         | Ordered list of wave records (see below).                                            |
| `worktree`   | string \| absent | Absolute realpath of the active worktree at init. Optional, absent on pre-#501 state files. Display/diagnostic metadata only—does not affect state file location keying or resume behavior. Used to scope the session-start banner to the active worktree. |
| `context`    | object        | Accumulated cross-wave context enabling fresh-session resume (see below).            |

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
| `tasks`       | array   | always                                   | Per-task records for this wave (see below).                          |

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
<stateDir>/<runId>/progress.json
```

- `<stateDir>` — `resolveStateDir()`'s return value, which already ends in `.sdlc/execution`. There is no additional `execution/` path segment: the marker sits directly inside the per-run directory, alongside that run's per-task fact sheets (`task-<id>.md`).
- `<runId>` — the run identifier passed to `execute_state({action:"wave-start", runId:...})` and threaded through the wave manifest.

One marker file covers the entire run, not one per wave — `tasks` is keyed by task ID across all waves, and the wave number plays no part in the file's path or its write/read call.

Example: `.sdlc/execution/run-20260328T143000Z/progress.json`

**Shape:**

```json
{
  "tasks": {
    "3": { "phase": "editing", "updatedAt": "2026-03-28T14:32:10Z" },
    "4": { "phase": "verifying", "updatedAt": "2026-03-28T14:32:40Z" }
  }
}
```

| Field                  | Type   | Description                                                                                    |
|------------------------|--------|--------------------------------------------------------------------------------------------------|
| `tasks`                | object | Keyed by task ID (string). One entry per task that has written at least one progress update.    |
| `tasks[id].phase`      | string | One of `started`, `reading`, `editing`, `verifying`, `reporting` — a free-text phase is rejected. |
| `tasks[id].updatedAt`  | string | ISO 8601 UTC timestamp of the most recent write for that task.                                   |

Written via `execute_state({action:"wave-progress", runId:<id>, taskId:<id>, phase:<phase>})` (atomic tmp-write + rename) — the action takes flat top-level fields, not a nested `payload` object, and does not accept a wave number at all. Read back via `execute_state({action:"wave-progress", runId:<id>, readProgress:true})`, which returns `{"tasks":{}}` when no marker exists yet rather than erroring. The per-run directory is not swept by the top-level state-file GC (which only scans `.json` files directly under `.sdlc/execution/`) — markers are reaped by `--gc` alongside the fact sheets in that same directory.

---

## Lifecycle Rules

### Cleanup

The state file is deleted automatically when execution completes successfully (all waves and tasks reach `completed` or `skipped`). This keeps `.sdlc/execution/` clean in the normal case.

If execution fails or is interrupted, the state file is retained so the run can be resumed.

### Resume

Passing `--resume` to `execute` causes it to locate the most recent state file for the current branch (matched by branch name in the filename). The skill then:

1. Skips any wave with status `completed`.
2. Retries any wave with status `in_progress` from its beginning (individual task results within that wave are not trusted — `execute_state({action:"resume-reset"})` clears those rows at resume time, before the resume pointer is computed).
3. Executes remaining waves with status `pending` normally.

The `context` object is loaded into the agent prompt so the resuming session understands what was built in prior waves.

If multiple state files exist for the same branch (from multiple failed attempts), the one with the most recent timestamp is used.

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
