# Plan-SDLC State File Format

The `plan` skill writes a JSON marker file to `.sdlc-v2/runs/` at the start of each planning invocation. This file records integrity checkpoints so the stop hook can verify that all quality gates were reached before the plan was presented. It also records the run's creation intent (for resuming after a context compaction) and its current step checkpoint. A separate per-run evidence directory (`<runId>.evidence/`) holds discovery/lane/lens findings — see [Evidence Directory](#evidence-directory) below.

---

## File Location

```
<main-worktree>/.sdlc-v2/runs/plan-<branch>-<timestamp>.json
```

- `<main-worktree>` — absolute path to the main git working tree (see [Worktree Safety](#worktree-safety) below)
- `<branch>` — current git branch name with `/` replaced by `-`
- `<timestamp>` — ISO 8601 UTC timestamp at prepare time, compacted to `YYYYMMDDTHHmmssZ`

Example: `.sdlc-v2/runs/plan-fix-my-bug-20260509T140000Z.json`

The filename pattern is recognized by the `internal/state` package's filename parser:

```
/^(ship|execute|plan)-(.+)-(\d{8}T\d{6}Z)\.json$/
```

---

## Worktree Safety

State files are always written to the **main working tree's** `.sdlc-v2/runs/`, not the current working directory. This ensures the marker is accessible regardless of whether plan runs inside a linked worktree.

**Main working tree resolution:** the `internal/state` package's worktree resolver runs `git worktree list --porcelain` and extracts the path from the first `worktree <path>` line.

---

## Top-Level Schema

```json
{
  "planIntegrity": {
    "skillInvoked":        "2026-05-09T14:00:00.000Z",
    "planFile":            "2026-05-09T14:02:10.000Z",
    "guardrailsEvaluated": "2026-05-09T14:05:30.000Z",
    "critiqueRan":         "2026-05-09T14:06:00.000Z"
  },
  "planFilePath": "/Users/dev/.claude/plans/2026-05-09-fix-auth.md",
  "creationIntent": {
    "userPrompt": "Fix the auth redirect bug",
    "scope": "full",
    "routing": "fileCount 4+ -> full (all steps)",
    "timestamp": "2026-05-09T14:00:00.000Z",
    "flags": {
      "fromOpenspec": "",
      "fromOpenspecDirect": false,
      "openspecInlineGenerate": false,
      "lightweight": false,
      "fileCount": 6
    }
  },
  "checkpoint": {
    "step": "3",
    "iteration": 1,
    "expectedWriters": ["lane-static-structural-r1"],
    "updatedAt": "2026-05-09T14:10:00.000Z"
  }
}
```

| Field           | Type             | Description                                                                 |
|-----------------|------------------|-----------------------------------------------------------------------------|
| `planIntegrity` | object           | Checkpoint markers; each key's presence means that checkpoint was reached.  |
| `planFilePath`  | string \| null   | Absolute path to the written plan file. Used by the Stop hook to stat the file for non-empty content verification. `null` until the `planFile` marker is written. |
| `creationIntent`| object           | The prompt and routing decision that started this run, plus flags needed to resume it. Written once by `plan_prepare` when the run is created; never written by `plan_mark`. See [creationIntent](#creationintent) below. |
| `checkpoint`    | object \| absent | The plan run's current step/iteration/expected-writers, replaced on every `plan_mark({marker: "checkpoint"})` call. Absent until the first checkpoint marker is written. See [checkpoint](#checkpoint) below. |

---

## Marker Fields

Each field inside `planIntegrity` is an ISO 8601 timestamp string. Absence of a key means that checkpoint was not reached.

| Marker                | Written when                                                                      |
|-----------------------|-----------------------------------------------------------------------------------|
| `skillInvoked`        | `plan_prepare(...)` at Step 0 prepare — plan was invoked (written automatically as a side effect; no separate `plan_mark` call) |
| `planFile`            | `plan_mark({ marker: "plan-file", path: <abs> })` after Step 0 path resolution    |
| `guardrailsEvaluated` | `plan_mark({ marker: "guardrailsEvaluated" })` at end of Step 3 guardrail gate    |
| `critiqueRan`         | `plan_mark({ marker: "critiqueRan" })` as final action of Step 3                  |

---

## creationIntent

Written once, by `plan_prepare` (function `fullCreationIntent`), the first time it creates a run. Never appended to or overwritten by `plan_mark`. A resumed run (`plan_prepare({resume: true})`) reads it back to restore the original prompt and flags (`applySavedIntent`), so a worker that only sees the resumed call still knows what the run was asked to do.

| Field       | Type   | Description                                                                 |
|-------------|--------|------------------------------------------------------------------------------|
| `userPrompt`| string | The user's original planning request.                                       |
| `scope`     | string | The complexity-routing pipeline mode chosen for this run: `full`, `lightweight`, or `skip`. |
| `routing`   | string | Human-readable reason string from the complexity-routing decision.          |
| `timestamp` | string | ISO 8601 UTC timestamp when the run was created.                            |
| `flags`     | object | Input flags needed to reproduce the same routing decision on resume. See below. |

`flags` sub-object:

| Field                    | Type    | Description                                                        |
|--------------------------|---------|----------------------------------------------------------------------|
| `fromOpenspec`           | string  | OpenSpec change name, or empty string when not planning from OpenSpec. |
| `fromOpenspecDirect`     | boolean | Whether the OpenSpec change was passed directly (not detected).      |
| `openspecInlineGenerate` | boolean | Whether inline OpenSpec generation was requested.                    |
| `lightweight`            | boolean | Whether the caller requested the lightweight pipeline override.      |
| `fileCount`              | integer | File count used for complexity routing.                              |

---

## checkpoint

Written by `plan_mark({ marker: "checkpoint", data: {...} })`. Unlike the `planIntegrity` markers, `checkpoint` is **replaced**, not appended, on every call — it always reflects the run's current position, not its history. Absent until the first checkpoint marker is written.

| Field             | Type     | Description                                                                 |
|-------------------|----------|-------------------------------------------------------------------------------|
| `step`            | string   | The SKILL.md step this checkpoint reflects. One of `0`, `1`, `2`, `3`, `4`, `5`, `6`, `6.5`, `6.6`, `7`. |
| `iteration`       | integer  | Iteration counter (used by the Step 5/6 review loop, which can repeat up to 3 times). |
| `expectedWriters` | string[] | Writer IDs the run is currently waiting on (e.g. dispatched lane or lens subagent IDs). Max 32 entries. |
| `updatedAt`       | string   | ISO 8601 UTC timestamp stamped by `plan_mark` at write time (not caller-supplied). |

The resume flow (`plan_prepare({resume: true})`) reads `checkpoint.step` to tell the orchestrator where to pick back up, and `checkpoint.expectedWriters` together with `plan_support({action: "evidence_digest"})` to tell it which writers it is still waiting on.

---

## Evidence Directory

Separate from the state file above, each plan run also owns a per-run evidence directory. It holds the discovery findings, lane/lens results, and guardrails snapshot that let a run resume after a context compaction without re-doing its research. It is written and read only through `plan_support`'s `evidence_record`, `evidence_digest`, and `evidence_get` actions (`internal/tools/plan_evidence.go`) — nothing reads or writes it directly.

### Layout

```
<main-worktree>/.sdlc-v2/runs/<runId>.evidence/
├── main.json          # the orchestrator's own recorded items, if any
├── <writerId>.json     # one file per dispatched writer (explorer, lane, lens, reviewer)
├── brief.md            # optional: the discovery brief, recorded by writerId "main" only
└── guardrails.md       # the guardrails snapshot, written by plan_prepare (not evidence_record)
```

`<runId>` is `state.RunID(st)` — the state file's stem, e.g. `plan-fix-my-bug-20260509T140000Z` (not just the timestamp). `guardrailsFile`, one of `plan_prepare`'s output fields, is this directory's `guardrails.md` path.

### Writer file format

Each `<writerId>.json` holds one writer's recorded findings:

```json
{
  "writerId": "lane-static-structural-r1",
  "status": "done",
  "updatedAt": "2026-05-09T14:03:00.000Z",
  "items": [
    {
      "id": "F-auth-1",
      "summary": "token check skips expiry",
      "ref": "internal/tools/plan.go:1185-1200",
      "body": "validateToken returns early before the exp claim is read."
    }
  ]
}
```

| Field       | Type   | Description                                                                    |
|-------------|--------|-----------------------------------------------------------------------------------|
| `writerId`  | string | The writer's own ID. `"main"` is reserved for the orchestrator session.           |
| `status`    | string | `running` or `done`.                                                              |
| `updatedAt` | string | ISO 8601 UTC timestamp of the last `evidence_record` call for this writer.        |
| `items`     | array  | Up to 200 items, max 64 KiB total file size. Each item: `id` (unique per writer, e.g. `F-auth-1`), `summary` (one line, max 200 chars), `ref` (optional, one line, max 500 chars — `path:line` or URL), `body` (optional Markdown, returned only by `evidence_get`). |

Writer files are capped at 32 per run (`evidenceMaxWriters`). `evidence_digest` reads every writer file's `status`/`updatedAt`/item summaries to report which writers are still `running`, which have gone stale (`stalledWriters`), and which expected writers never wrote a file at all (`missingWriters`).

---

## Lifecycle Rules

### Write

1. On a genuinely new run (no active run for the branch, and not a `resume: true` call), `plan_prepare(...)` calls the `internal/state` package's prune helper to remove all prior `plan-<branchSlug>-*.json` files for the same branch (at most one plan marker per branch exists between invocations), then writes the new marker atomically with `planIntegrity: { skillInvoked: <ISO-ts> }` and `creationIntent: { userPrompt, timestamp }`. It then best-effort prunes stale `<runId>.evidence/` directories left by earlier runs.
2. The first `resolveTemplate: true` call for that run overwrites `creationIntent` with the full shape (`fullCreationIntent`: userPrompt, scope, routing, timestamp, flags).
3. A `resume: true` call does not create or prune anything; it reads the branch's active run back (`state.ActivePlanRun`) and restores `creationIntent` into the caller's input (`applySavedIntent`) instead of overwriting it.
4. Subsequent `plan_mark({ marker, path })` calls update the `planIntegrity` keys and `planFilePath` in-place, atomically; `plan_mark({ marker: "checkpoint", data })` replaces `checkpoint` in-place, atomically.

### Consume-then-Delete

The stop hook runs at session end:

1. Calls `findStateFile('plan', branchSlug)` and captures the returned path.
2. Reads the marker via `readState`.
3. If the `done` marker is absent, the plan is still running (e.g. a Stop fired mid-plan across a compaction): returns silently, with no evaluation and no deletion.
4. Once `done` is present: deletes the state file **and** its per-run evidence directory (`state.EvidenceDir(st.Root, state.RunID(st))`), regardless of integrity outcome — both are single-use.
5. Evaluates all five `planIntegrity` keys and stats `planFilePath`; any missing or failing marker produces one aggregated warning, never a block.
6. Subsequent Stop events on the same branch engage the transcript-fallback path (R21) because no marker exists.

Both removals are wrapped so a failure cannot break the hook's advisory-only exit-0 contract.

### GC Orphan Sweep

Stale plan markers (abandoned sessions, branch-deleted, TTL-expired) are removed by `ship --gc` and `execute --gc` via `gcStateFiles({ prefix: 'plan', ttlDays, knownBranches })`. The sweep reports plan-prefix files in a `plan` bucket alongside the existing `ship` and `execute` buckets in the JSON output. `gcStateFiles` does not touch evidence directories — those are cleaned only by the Stop hook (on `done`) and by `state.PruneEvidenceDirs` (on the next new run for the same branch).

### Atomic Write

All writes use the `internal/state` package's atomic-write helper. No partial-file states are possible.

---

## Full Example

```json
{
  "planIntegrity": {
    "skillInvoked":        "2026-05-09T14:00:05.123Z",
    "planFile":            "2026-05-09T14:02:11.456Z",
    "guardrailsEvaluated": "2026-05-09T14:05:33.789Z",
    "critiqueRan":         "2026-05-09T14:06:01.012Z"
  },
  "planFilePath": "/Users/dev/.claude/plans/2026-05-09-fix-auth.md",
  "creationIntent": {
    "userPrompt": "Fix the auth redirect bug",
    "scope": "full",
    "routing": "fileCount 4+ -> full (all steps)",
    "timestamp": "2026-05-09T14:00:05.123Z",
    "flags": {
      "fromOpenspec": "",
      "fromOpenspecDirect": false,
      "openspecInlineGenerate": false,
      "lightweight": false,
      "fileCount": 6
    }
  },
  "checkpoint": {
    "step": "5",
    "iteration": 2,
    "expectedWriters": ["lens-architecture-r1", "lens-requirements-r1", "lens-risk-r1"],
    "updatedAt": "2026-05-09T14:06:30.000Z"
  }
}
```

A marker file with only `skillInvoked` and the bootstrap `creationIntent` set (plan was invoked but crashed before writing the plan file):

```json
{
  "planIntegrity": {
    "skillInvoked": "2026-05-09T14:00:05.123Z"
  },
  "planFilePath": null,
  "creationIntent": {
    "userPrompt": "Fix the auth redirect bug",
    "timestamp": "2026-05-09T14:00:05.123Z"
  }
}
```

The Stop hook would report `planFile`, `guardrailsEvaluated`, and `critiqueRan` as missing checkpoints.
