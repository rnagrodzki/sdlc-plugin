# Pipeline State File Format

`ship` persists pipeline progress through the `ship_state` MCP tool (`internal/tools/ship_state.go`), a single tool with a 16-value `action` parameter. This document describes the on-disk JSON shape that tool reads and writes, so pipeline prose can be verified against the real contract instead of assumed from the JS source this skill was ported from.

Every `ship_state` call takes `{action, step?, detail?, sessionId?}`. Action-specific parameters (`result`, `reason`, `error`, `text`, `severity`, `file`, `title`, `line`, `from`, `to`, `force`, `ttlDays`, `branch`, `outcome`, ...) always travel inside `detail` as a nested object — never as top-level fields alongside `action`.

---

## File Location

```
.sdlc-v2/execution/ship-<branch-slug>-<timestamp>.json
```

Managed by the shared `internal/state` package (the same one `execute_state`, `plan_state`, and `commit`'s state helpers use). The skill never constructs or parses this filename itself — every action resolves the file by current branch (or by an explicit `detail.branch` / `detail.stateFile`) and returns already-parsed JSON.

---

## Top-Level Schema

```json
{
  "version": 1,
  "startedAt": "2026-03-27T14:30:00Z",
  "branch": "feat/ship",
  "worktree": "/Users/you/repo",
  "sessionId": "abc123",
  "flags": { ... },
  "steps": [ ... ],
  "decisions": [ ... ],
  "deferredFindings": [ ... ],
  "issues": [ ... ],
  "lastFailedStep": null,
  "sideEffects": { ... },
  "pipelineStatus": "completed",
  "pipelineCompletedAt": "2026-03-27T15:10:00Z"
}
```

| Field | Type | Description |
|---|---|---|
| `version` | number | Always `1`. |
| `startedAt` | string | ISO 8601 UTC timestamp, set at pipeline init. |
| `branch` | string | Git branch name at pipeline start. |
| `worktree` | string | Absolute path of the working directory at init. |
| `sessionId` | string | The `sessionId` passed to init (or a later action that re-claims the run). |
| `flags` | object | The resolved pipeline configuration `ship_prepare` merged — see below. |
| `steps` | array | One entry per configured step. See "The `steps[]` scaffold" below — this is config-driven, not a fixed list. |
| `decisions` | array | Appended by `ship_state{action:"decide"}`. |
| `deferredFindings` | array | Appended by `ship_state{action:"defer"}`. |
| `issues` | array | Structured issue accumulator, appended by `ship_state{action:"fail"}`. See "Issues and `lastFailedStep`" below. |
| `lastFailedStep` | string \| null | Name of the most recent step passed to `fail`. |
| `sideEffects` | object | Idempotency journal keyed by step name. Written by `ship_verify_side_effect`; consulted by `begin-step`'s `alreadyDone` flag. See below. |
| `pipelineStatus` | string | Absent until the pipeline is stamped terminal. Set to `"completed"` by `cleanup`/`cleanup-pipeline` — see "Lifecycle: Cleanup." |
| `pipelineCompletedAt` | string | Paired timestamp, set alongside `pipelineStatus`. |

There is no `nextPendingStep` field written into the file. The closest equivalent is `ship_state{action:"next"}`, a live query (see "The `next` action" below) — not a stored field.

---

## `flags` Object

`ship_prepare` writes its own resolved `merged` flags object here verbatim when it initializes the run — the same values reported in its own output (`auto`, `steps` — the full resolved canonical step-name list, `bump`, `draft`, `reviewThreshold`, `rebase`, and the rest of the merged config). It is an opaque map as far as `ship_state` itself is concerned, not a validated struct; there is no fixed field list enforced by the tool. `flags.steps` is what actually drives the `steps[]` scaffold below — the two must always agree, since both come from the same `ship_prepare` call.

---

## The `steps[]` Scaffold

`ship_prepare` — the real, sole entry point this skill uses to start a run — seeds `steps[]` from `shipmeta.InitialShipStepsFromConfig(stepsList)`, where `stepsList` is the same resolved step list written to `flags.steps`. **This is config-driven: one entry per configured step name, nothing more, nothing less.** A pipeline configured with 6 steps seeds 6 entries; one configured with 10 seeds 10.

```json
[
  { "name": "execute",           "status": "pending", "kind": "tracked" },
  { "name": "commit",            "status": "pending", "kind": "tracked" },
  { "name": "review",            "status": "pending", "kind": "tracked" },
  { "name": "version",           "status": "pending", "kind": "tracked" },
  { "name": "archive-openspec",  "status": "pending", "kind": "inline"  },
  { "name": "pr",                "status": "pending", "kind": "tracked" }
]
```

(This example reflects `steps: ["execute","commit","review","version","archive-openspec","pr"]`; substitute the project's actual configured list.)

Every entry carries a `kind`: `"tracked"` for the five step names with a purpose-built dispatch shape in this skill (`execute`, `commit`, `review`, `version`, `pr`), `"inline"` for everything else that can appear in `ship.steps[]` (`verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, `learnings-commit`). **`kind` only signals which dispatch style the skill's own prose uses for that step — Agent-dispatched sub-skill for `tracked`, done in this skill's own prose for `inline` — it does NOT mean "no `steps[]` entry" or "no lifecycle."** Both kinds get a real entry and both require the same `begin-step` → `complete-step`/`skip`/`fail` lifecycle calls, or the entry sits at `pending` forever and blocks `next` and the cleanup contract check (see below).

> **Known divergence:** the schema's own doc comment for `kind` describes `"inline"` as "recorded via the generic decide action" — this reads as if `decide` replaces the lifecycle calls for inline steps. It does not. `decide` only appends a free-text note to `decisions[]`; it never looks up or mutates a `steps[]` entry (confirmed by reading `shipStateDecide`'s full body — it has no step-lookup at all). An inline step still needs `begin-step`/`complete-step` (or `skip`/`fail`) exactly like a tracked step. Treat the schema comment's phrasing as legacy/misleading, not as the operative contract; this document and `reference.md` describe the actual behavior.

### The two names with no entry at all

`received-review` and `commit-fixes` are **never** members of `ship.steps[]` / `flags.steps` — they are conditional sub-steps triggered by a review verdict, not pipeline-composition choices (see `shipmeta.CanonicalSteps`, which excludes both). Because `InitialShipStepsFromConfig` only creates an entry for names present in `stepsList`, these two **never get a `steps[]` entry, under any real `ship_prepare`-driven run.** `begin-step`, `complete-step`, `start`, `complete`, `skip`, and `fail` all look a step up by name (`shipFindStepEntry`, a plain linear scan) and return a `DataError` ("step %q not found in state") for either name. Track their outcome with `ship_state{action:"decide", step:"received-review"|"commit-fixes", detail:{text:"..."}}` instead — `decide` never validates against `steps[]`, so it always succeeds.

### A legacy raw scaffold still exists, but this skill never uses it

The raw `ship_state{action:"init"}` action (distinct from `ship_prepare`, which this skill always calls instead) still seeds the **old fixed 7-entry scaffold** (`shipmeta.InitialShipSteps()`) for backward byte-compatibility with pre-existing callers/tests:

```json
[
  { "name": "execute",          "status": "pending" },
  { "name": "commit",           "status": "pending" },
  { "name": "review",           "status": "pending" },
  { "name": "received-review",  "status": "pending", "condition": "if critical/high findings" },
  { "name": "commit-fixes",     "status": "pending", "condition": "if received-review made changes" },
  { "name": "version",          "status": "pending" },
  { "name": "pr",               "status": "pending" }
]
```

This scaffold's entries carry no `kind` field at all (omitted) and — uniquely — give `received-review`/`commit-fixes` a `condition` key, which is what let them rest at `pending` without blocking R-b1 below. **This skill never calls raw `init` — it always starts a run via `ship_prepare`.** Do not describe this fixed scaffold as what a real run looks like; it is documented here only so a reader who encounters an old state file (or a test fixture) is not confused by the difference.

### Step Fields

| Field | Type | Present when | Description |
|---|---|---|---|
| `name` | string | always | One of the 12 known step names. |
| `status` | string | always | See Status Values below. |
| `kind` | string | `ship_prepare`-driven runs only | `"tracked"` or `"inline"` — dispatch-style hint, not a lifecycle exemption (see above). |
| `startedAt` | string | status is `in_progress` | Set by `begin-step`. |
| `completedAt` | string | status is `completed` or `skipped` | Set by `complete-step` / `skip`. |
| `result` | any | status is `completed` and `detail.result` was passed | Free-form value from `detail.result`. |
| `condition` | string | only on the legacy raw-`init` scaffold's `received-review`/`commit-fixes` entries | Never set by `InitialShipStepsFromConfig` — a real `ship_prepare`-driven run has **no entry that ever carries a `condition` key**. |
| `reason` | string | status is `skipped` and `detail.reason` was passed | Why the step was skipped. |
| `error` | any | status is `failed` and `detail.error` was passed | Failure detail from `detail.error`. |

### Status Values

| Status | Meaning |
|---|---|
| `pending` | Not started. Blocks progress unless the step also carries a `condition` key (R-b1). |
| `in_progress` | Currently executing; always blocks progress. |
| `completed` | Finished successfully; never blocks. |
| `skipped` | Intentionally bypassed; never blocks. |
| `failed` | Terminated with an error; always blocks progress (and never trips the cleanup contract check — see below). |

**R-b1 proceed-gate** (`shipStepBlocksProceed`): a `pending` step blocks unless it has a `condition` key present. Under a real `ship_prepare`-driven run this exemption is **effectively dead code** — no entry ever carries `condition`, since `InitialShipStepsFromConfig` never sets it. Every entry must be driven to `completed`/`skipped`/`failed` before `next`/`begin-step`'s prior-step check will move past it. (The exemption only matters for the legacy fixed scaffold's `received-review`/`commit-fixes` entries, which this skill never produces.)

---

## The `next` Action

`ship_state{action:"next"}` returns `{step?, automation?}`: it walks `steps[]` **in the order the entries were scaffolded** (i.e. the order the pipeline was configured in), returning the name of the first entry where R-b1 says progress is blocked, or an empty response when every entry is terminal. `automation` resolves via the project's `automation.mode`/`automation.steps` config (see `config-format.md`), defaulting to `"confirm"` on any config-read failure.

Because `steps[]` is now config-driven (see above), `next` walks **every** configured step in order — `verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, and `learnings-commit` are all visible to it when configured, not skipped. The one caveat carried over from the scaffold gap: `received-review` and `commit-fixes` are never in `steps[]` at all, so `next` never names them — they are conditional sub-steps this skill's own prose dispatches directly (based on the review verdict), not something to wait on `next` for. An empty `next` result means every *configured* step is terminal; it does not by itself distinguish "pipeline actually done" from "conditional review-fix loop still pending a verdict" — that judgment stays with the skill's own review-verdict handling.

---

## `ship_state{action:"todos"}`

Returns `{todos: [{content, activeForm, status}, ...]}`, rendered from `flags.steps` (plus an always-appended synthetic `"cleanup"` entry) joined with whatever status each name has in `steps[]`. Since every configured name now gets a real `steps[]` entry (see above), a configured inline step (`verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, `learnings-commit`) renders its **real, current** status here — not a permanent `"pending"`. The only names that always render `"pending"` regardless of what actually happened are `received-review` and `commit-fixes`, since they never have a `steps[]` entry to join against. Use `todos` for the task-tray checklist; do not use it to gate pipeline logic for `received-review`/`commit-fixes` — check `decisions[]` for those instead.

---

## `decisions` Array

Appended by `ship_state{action:"decide", step, detail:{text}}`. Never overwritten, and never validated against `steps[]` — `step` can be any name, tracked or inline, configured or not (it's most useful for `received-review`/`commit-fixes`, which have no other way to record an outcome, but nothing stops calling it for a tracked step too, e.g. to leave a supplementary note).

```json
{ "step": "verify-openspec", "decision": "openspec validate --strict: passed" }
```

| Field | Type | Description |
|---|---|---|
| `step` | string | The `step` value passed to `decide`. |
| `decision` | string | The `detail.text` value passed. |

---

## `deferredFindings` Array

Appended by `ship_state{action:"defer", detail:{severity, file, title, line?}}`. `severity`, `file`, and `title` are required by the tool (a `DomainError` otherwise); `line` is passed through as-is (including `null`).

```json
{ "severity": "medium", "file": "src/auth.ts", "line": 42, "title": "Extract token validation" }
```

Only `medium` and `low` findings should be deferred this way. `critical`/`high` findings are expected to route through `received-review` instead.

---

## Issues and `lastFailedStep`

`ship_state{action:"fail", step, detail:{reason?, error?, severity?, category?}}` — besides setting the target step's `status:"failed"` — also sets `st.Data["lastFailedStep"]` to the failed step's name and appends a structured entry to `issues[]`:

```json
{
  "step": "review",
  "severity": "error",
  "category": "ship-fail",
  "summary": "...",
  "detail": "...",
  "timestamp": "2026-03-27T14:45:00Z"
}
```

`wave`, `step`, `taskId`, and `detail` are optional; `severity`, `category`, `summary`, and `timestamp` are always present. This mirrors `execute-state.schema.json`'s own `issues[]` shape.

`complete-step`'s response also carries `issueCount` (total issues recorded so far) and `issueHighlights` (up to a handful of the most recent `"[severity] summary"` strings) — read those off the tool's response directly rather than re-deriving them from `issues[]` yourself.

---

## `sideEffects` Object

Idempotency journal keyed by step name, recording each step's verified git/PR side effect so a resumed pipeline can skip re-doing work that already landed:

```json
{
  "pr": { "kind": "pr", "ref": "https://github.com/org/repo/pull/42", "verifiedAt": "2026-03-27T15:00:00Z" }
}
```

`kind` is one of `"tag"`, `"pr"`, or `"sha"`. Written by `ship_verify_side_effect`; consulted by `begin-step`'s `alreadyDone` flag (surfaced in `ShipStepNarrationOut.AlreadyDone`) so a resumed pipeline doesn't, say, re-push a tag that already landed.

---

## Lifecycle: Cleanup

Two actions, both terminal, neither a `steps[]` entry. **Neither deletes the state file.** Both stamp it terminal and leave it in place — it survives for later reads (including a subsequent `--resume` attempt, which will correctly report no run in flight) until GC's TTL prunes it.

- **`ship_state{action:"cleanup", detail:{branch?}}`** — validates the pipeline contract (every `steps[]` entry, tracked or inline, must be `completed`, `skipped`, or `failed`; a `pending` entry, or any `in_progress` entry, is a violation — `failed` is never a violation), then stamps `pipelineStatus:"completed"` + `pipelineCompletedAt:<timestamp>` via `state.Write` and returns `{"valid":true,"cleaned":true,"pipelineStatus":"completed","pipelineCompletedAt":"..."}`. No state file found is a silent no-op returning `{}`. A contract violation returns a `DataError` listing the violating steps; the file is left completely untouched (not stamped).

- **`ship_state{action:"cleanup-pipeline", detail:{branch?, force?, ttlDays?}}`** — the action this skill's Terminal Cleanup step calls. Three paths for `currentRun`:
  - `force:true` — skips the contract check entirely and does **not** stamp anything: `{"cleaned":false,"preservedReason":"force"}`.
  - no state file found — `{"valid":true,"cleaned":false,"reason":"no-state-file"}`.
  - otherwise — validates the contract exactly like `cleanup` (same violation semantics) and, on success, stamps exactly like `cleanup`: `{"valid":true,"cleaned":true,"pipelineStatus":"completed","pipelineCompletedAt":"..."}`. A violation still returns a `DataError` and stops here — the GC sweep below does not run.

  All three non-violation paths then run an unconditional GC sweep and a per-run-directory reap, returning:
  ```json
  {
    "currentRun": { "...one of the three shapes above..." },
    "gc": { "ship": {...}, "execute": {...}, "plan": {...}, "commit": {...} },
    "directories": { "deleted": [...], "kept": [...], "ledger": { "deleted": [...], "kept": [...] } },
    "force": false,
    "ttlDays": 14
  }
  ```
  `directories` reaps stale per-run execute directories and their ledger subdirectory (keyed off `execute-*.json` state files' `startedAt`) — this is the run-dir/ledger cleanup that happens alongside, not instead of, the state-file stamp.

Because contract validation only inspects `steps[]` entries, `received-review`/`commit-fixes` (which never get an entry) can never trip this check either way — their outcome is invisible to `cleanup`/`cleanup-pipeline`, tracked only in `decisions[]`.

Note: `ship_state{action:"gc"}` (no `-pipeline` suffix) is a different, narrower action used internally by other lifecycle paths — it is **not** the same report shape as `ship_prepare{gc:true}`, which is what the `--gc` CLI entry-mode calls (see `entry-modes.md`). Do not conflate the three.

---

## Resume

`ship_state{action:"read"}` is the native resume-detection call — do not hand-compute a resume point from `next` plus a `decisions[]` scan. When a run is genuinely in flight, the response carries a `resumeBriefing` block:

```
resumable        bool
lastStep         string
lastStepStatus   string   // a "failed" step still reports resumable:true, never an error
sideEffects      object
summary          string
display          string   // markdown — render verbatim, do not paraphrase
timing           {stepSeconds, pipelineSeconds, idleSeconds, human}
next             string   // the step to resume from
```

`resumeBriefing` is **absent** — meaning "nothing to resume, fall through to a fresh start" — in three cases: no state file exists for the branch; the only file found is already stamped terminal (`pipelineStatus:"completed"`); or a state file exists but no step has ever actually started (nothing was ever in flight to resume). All three are safe to treat identically: proceed to the normal fresh-start path (`ship_prepare`), whose own orphan-pruning removes the stale/empty file as a side effect of writing the new one.

A step left at `in_progress` at resume time should be retried from the beginning of that step, not assumed complete.
