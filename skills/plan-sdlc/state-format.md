# Plan-SDLC State File Format

The `plan-sdlc` skill writes a JSON marker file to `.sdlc/execution/` at the start of each planning invocation. This file records integrity checkpoints so the `stop-plan-integrity.js` Stop hook can verify that all quality gates were reached before the plan was presented.

---

## File Location

```
<main-worktree>/.sdlc/execution/plan-<branch>-<timestamp>.json
```

- `<main-worktree>` — absolute path to the main git working tree (see [Worktree Safety](#worktree-safety) below)
- `<branch>` — current git branch name with `/` replaced by `-`
- `<timestamp>` — ISO 8601 UTC timestamp at prepare time, compacted to `YYYYMMDDTHHmmssZ`

Example: `.sdlc/execution/plan-fix-my-bug-20260509T140000Z.json`

The filename pattern is recognized by the `internal/state` package's filename parser:

```
/^(ship|execute|plan)-(.+)-(\d{8}T\d{6}Z)\.json$/
```

---

## Worktree Safety

State files are always written to the **main working tree's** `.sdlc/execution/`, not the current working directory. This ensures the marker is accessible regardless of whether plan-sdlc runs inside a linked worktree.

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
  "planFilePath": "/Users/dev/.claude/plans/2026-05-09-fix-auth.md"
}
```

| Field          | Type             | Description                                                                 |
|----------------|------------------|-----------------------------------------------------------------------------|
| `planIntegrity`| object           | Checkpoint markers; each key's presence means that checkpoint was reached.  |
| `planFilePath` | string \| null   | Absolute path to the written plan file. Used by the Stop hook to stat the file for non-empty content verification. `null` until the `planFile` marker is written. |

---

## Marker Fields

Each field inside `planIntegrity` is an ISO 8601 timestamp string. Absence of a key means that checkpoint was not reached.

| Marker                | Written when                                                                      |
|-----------------------|-----------------------------------------------------------------------------------|
| `skillInvoked`        | `plan_prepare(...)` at Step 0 prepare — plan-sdlc was invoked (written automatically as a side effect; no separate `plan_mark` call) |
| `planFile`            | `plan_mark({ marker: "plan-file", path: <abs> })` after Step 0 path resolution    |
| `guardrailsEvaluated` | `plan_mark({ marker: "guardrailsEvaluated" })` at end of Step 3 guardrail gate    |
| `critiqueRan`         | `plan_mark({ marker: "critiqueRan" })` as final action of Step 3                  |

---

## Lifecycle Rules

### Write

1. `plan_prepare(...)` calls the `internal/state` package's prune helper to remove all prior `plan-<branchSlug>-*.json` files for the same branch (at most one plan marker per branch exists between invocations).
2. `plan_prepare(...)` then writes the new marker atomically with `planIntegrity: { skillInvoked: <ISO-ts> }`.
3. Subsequent `plan_mark({ marker, path })` calls update the `planIntegrity` keys and `planFilePath` in-place, atomically.

### Consume-then-Delete

`hooks/stop-plan-integrity.js` runs at session end (Stop hook):

1. Calls `findStateFile('plan', branchSlug)` and captures the returned path.
2. Reads the marker via `readState`.
3. Evaluates all four `planIntegrity` keys and stats `planFilePath`.
4. Calls `deleteState(path)` **regardless of integrity outcome** — the marker is single-use.
5. Subsequent Stop events on the same branch engage the transcript-fallback path (R21) because no marker exists.

The `deleteState` call is wrapped in a try/catch; a failed unlink cannot break the hook's advisory-only exit-0 contract.

### GC Orphan Sweep

Stale plan markers (abandoned sessions, branch-deleted, TTL-expired) are removed by `ship-sdlc --gc` and `execute-plan-sdlc --gc` via `gcStateFiles({ prefix: 'plan', ttlDays, knownBranches })`. The sweep reports plan-prefix files in a `plan` bucket alongside the existing `ship` and `execute` buckets in the JSON output.

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
  "planFilePath": "/Users/dev/.claude/plans/2026-05-09-fix-auth.md"
}
```

A marker file with only `skillInvoked` set (plan-sdlc was invoked but crashed before writing the plan file):

```json
{
  "planIntegrity": {
    "skillInvoked": "2026-05-09T14:00:05.123Z"
  },
  "planFilePath": null
}
```

The Stop hook would report `planFile`, `guardrailsEvaluated`, and `critiqueRan` as missing checkpoints.
