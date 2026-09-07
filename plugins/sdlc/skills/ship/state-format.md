# Pipeline State File Format

`ship` persists pipeline progress through the `ship_state` MCP tool (`internal/tools/ship_state.go`), a single tool with a 16-value `action` parameter. This document describes the on-disk JSON shape that tool reads and writes, so pipeline prose can be verified against the real contract instead of assumed from the JS source this skill was ported from.

Every `ship_state` call takes `{action, step?, detail?, sessionId?}`. Action-specific parameters (`result`, `reason`, `error`, `text`, `severity`, `file`, `title`, `line`, `from`, `to`, `force`, `ttlDays`, `branch`, `outcome`, ...) always travel inside `detail` as a nested object — never as top-level fields alongside `action`.

---

## File Location

```
.sdlc/execution/ship-<branch-slug>-<timestamp>.json
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
  "deferredFindings": [ ... ]
}
```

| Field | Type | Description |
|---|---|---|
| `version` | number | Always `1`. |
| `startedAt` | string | ISO 8601 UTC timestamp, set by `ship_state{action:"init"}`. |
| `branch` | string | Git branch name at pipeline start. |
| `worktree` | string | Absolute path of the working directory at init (`workDir` at the time `init` ran). |
| `sessionId` | string | The `sessionId` passed to `init` (or a later action that re-claims the run). |
| `flags` | object | Whatever object the caller passed as `detail.flags` to `init` — see below. |
| `steps` | array | **Only the seven step names in the table below** — see "The `steps[]` scaffolding gap." |
| `decisions` | array | Appended by `ship_state{action:"decide"}`. |
| `deferredFindings` | array | Appended by `ship_state{action:"defer"}`. |

There is no `nextPendingStep` field written into the file. The closest equivalent is `ship_state{action:"next"}`, a live query (see "The `next` action" below) — not a stored field.

---

## `flags` Object

`ship_state{action:"init"}` stores `detail.flags` verbatim (defaulting to `{}` if omitted) — it is an opaque map as far as the tool is concerned, not a validated struct. The ported `ship/SKILL.md` should populate it with the same resolved values `ship_prepare`'s output already computed, so a later `--resume` sees the flags that actually drove the run: typically `auto` (bool), `steps` (the resolved canonical step-name list, not a legacy `skip`/`preset` pair — see `config-format.md`), `bump`, `draft`, and any other `ship_prepare` output fields worth replaying on resume. There is no fixed field list enforced by the tool; treat this as a call-site convention, not a contract to test against.

---

## `steps[]` Array and the Scaffolding Gap

`ship_state{action:"init"}` does **not** derive `steps[]` from the pipeline's configured step list (`flags.steps` / `ship.steps[]`). It unconditionally seeds a fixed 7-entry scaffold (`shipmeta.InitialShipSteps()`, `internal/shipmeta/fields.go`), the same regardless of which steps the run actually configured:

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

**`verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, and `learnings-commit` never get an entry in `steps[]`, at any point in the run.** No code path adds them. This is a real, disclosed limitation of the current `ship_state` tool, not a design choice made by this documentation — see `reference.md`'s Gotchas section.

Concretely, this splits the 13 step names known to `shipmeta.SubstepMap` (the substep-rendering table behind `todos`; distinct from `shipmeta.CanonicalSteps`, the narrower 10-entry list that validates `ship.steps[]`/`--steps` and excludes `received-review`, `commit-fixes`, and `cleanup` as pipeline-composition choices) into two groups with different tracking mechanics:

### Scaffolded steps — tracked in `steps[]`

`execute`, `commit`, `review`, `received-review`, `commit-fixes`, `version`, `pr`. These are the only names `ship_state{action:"start"}` / `"begin-step"` / `"complete"` / `"complete-step"` / `"skip"` / `"fail"` will accept — each of those six actions looks the step up by name (`shipFindStepEntry`) and returns a `DataError` ("step %q not found in state") for any name outside this list. `ship_state{action:"next"}` also only walks this 7-entry array when deciding what is left to run.

### Inline steps — never in `steps[]`

`verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, `learnings-commit`. **Never call `start`, `begin-step`, `complete`, `complete-step`, `skip`, or `fail` with one of these five names — the call will error.** The ported `ship/SKILL.md` sequences these steps directly in its own prose instead:
- Progress is recorded with `ship_state{action:"decide", step:"<name>", detail:{text:"<what happened>"}}` (no step-entry lookup — always succeeds).
- User-visible progress (task-tray todos) is driven by Claude Code's own `TodoWrite` tool, called directly from the pipeline's main thread, not derived from a `steps[]` status.
- `ship_state{action:"todos"}` still returns a rendered checklist covering the full configured step list (it reads `flags.steps` independently of `steps[]` — see below), but an inline step will render as `"pending"` in that checklist even after it finishes, since its status was never recorded anywhere `todos` reads from. Do not rely on `todos` output to detect completion of an inline step.

`cleanup` is not a `steps[]` entry either, at any point — it is a terminal action (`ship_state{action:"cleanup"}` / `"cleanup-pipeline"}`), not a tracked pipeline step. See "Terminal cleanup" below.

### Step Fields (scaffolded steps only)

| Field | Type | Present when | Description |
|---|---|---|---|
| `name` | string | always | One of the seven scaffolded names above. |
| `status` | string | always | See Status Values below. |
| `startedAt` | string | status is `in_progress` | Set by `start` / `begin-step`. |
| `completedAt` | string | status is `completed` or `skipped` | Set by `complete` / `complete-step` / `skip`. |
| `result` | any | status is `completed` and `detail.result` was passed | Free-form value from `detail.result`. |
| `condition` | string | `received-review`, `commit-fixes` | Fixed natural-language string from `InitialShipSteps()`; its mere presence is what lets the step rest at `pending` without blocking progress (see R-b1 below). |
| `reason` | string | status is `skipped` and `detail.reason` was passed | Why the step was skipped. |
| `error` | any | status is `failed` and `detail.error` was passed | Failure detail from `detail.error`. |

Source's `reviewVerdict` / `prUrl` / `commitSha` / `versionTag` / `failedReason` fields are not separately modeled by `ship_state` — if the ported skill wants to preserve one of these as structured data, pass it inside `detail.result` (a free-form value) and note it there, or record it via `ship_state{action:"decide"}`.

### Status Values

| Status | Meaning |
|---|---|
| `pending` | Not started. Blocks progress unless the step also carries a `condition` key (R-b1). |
| `in_progress` | Currently executing; always blocks progress. |
| `completed` | Finished successfully; never blocks. |
| `skipped` | Intentionally bypassed; never blocks. |
| `failed` | Terminated with an error; always blocks progress. |

**R-b1 proceed-gate** (`shipStepBlocksProceed`, `ship_state.go`): a `pending` step blocks unless it has a `condition` key present — this is exactly how `received-review` and `commit-fixes` are allowed to sit at `pending` indefinitely (they are conditional; not every run triggers them) without stalling `begin-step`'s prior-step check or `next`.

---

## The `next` Action

`ship_state{action:"next"}` returns `{step?, automation?}`: the first scaffolded step where R-b1 says progress is blocked, or an empty response when all seven scaffolded steps are terminal. `automation` resolves via the project's `automation.mode`/`automation.steps` config (see `config-format.md`), defaulting to `"confirm"` on any config-read failure.

Two disclosed hazards follow directly from the scaffolding gap above:
1. `next` can name `"pr"` as the step to run immediately after `"version"` completes, even when `verify-openspec` and/or `archive-openspec` are configured to run between them — those two names are invisible to `next` because they have no `steps[]` entry. The pipeline's own step order (from `flags.steps`, not from `next`) is what actually governs sequencing; `next` is a resume-entry helper over the seven tracked steps, not the pipeline's authoritative execution order.
2. `next` reports "nothing left" (empty `step`) once `execute` through `pr` are all terminal, even if `verify-pipeline`, `await-remote-review`, or `learnings-commit` are configured and have not run yet. The ported skill must keep dispatching those from its own inline prose after `next` goes empty — it must not treat an empty `next` result as "pipeline complete."

---

## `ship_state{action:"todos"}`

Returns `{todos: [{content, activeForm, status}, ...]}`, rendered from **`flags.steps`** (the full configured step list, independent of `steps[]`) joined with whatever status each name happens to have in `steps[]`. A name absent from `steps[]` — i.e. any of the five inline steps — always renders as `"pending"` unless it is the step name currently passed as `step` in the same call, in which case it renders `"in_progress"`. This degrades gracefully (it never errors, unlike `start`/`complete`/`skip`/`fail`) but does not reflect real completion for inline steps. Use it for the task-tray checklist; do not use it to gate pipeline logic for inline steps.

---

## `decisions` Array

Appended by `ship_state{action:"decide", step, detail:{text}}`. Never overwritten.

```json
{ "step": "verify-openspec", "decision": "openspec validate --strict: passed" }
```

| Field | Type | Description |
|---|---|---|
| `step` | string | The `step` value passed to `decide`. May be one of the five inline step names — `decide` never validates against `steps[]`. |
| `decision` | string | The `detail.text` value passed. |

---

## `deferredFindings` Array

Appended by `ship_state{action:"defer", detail:{severity, file, title, line?}}`. `severity`, `file`, and `title` are required by the tool (a `DomainError` otherwise); `line` is passed through as-is (including `null`).

```json
{ "severity": "medium", "file": "src/auth.ts", "line": 42, "title": "Extract token validation" }
```

Only `medium` and `low` findings should be deferred this way. `critical`/`high` findings are expected to route through `received-review` instead.

---

## Lifecycle: Cleanup

Two actions, both terminal, neither a `steps[]` entry:

- **`ship_state{action:"cleanup", detail:{branch?}}`** — validates the pipeline contract (every scaffolded step must be `completed`, `skipped`, or `failed` — a `pending` step with no `condition`, or any `in_progress` step, is a violation), then deletes the state file. No-op success if no state file is found. Returns a `DataError` listing violations if the contract check fails; the file is left in place for `--resume`.
- **`ship_state{action:"cleanup-pipeline", detail:{branch?, force?, ttlDays?}}`** — the action `ship`'s Terminal Cleanup step should call. Three paths: `force:true` skips the contract check entirely and preserves the state file (`{"cleaned":false,"preservedReason":"force"}`); no state file found is a no-op (`{"cleaned":false,"reason":"no-state-file"}`); otherwise it validates the contract exactly like `cleanup` and deletes the file on success. **All three paths** then run an unconditional GC sweep across ship/execute/plan/commit state directories and return `{currentRun, gc:{ship, execute, plan, commit}, force, ttlDays}`.

Because contract validation only inspects the seven scaffolded `steps[]` entries, an inline step (e.g. `archive-openspec`) left conceptually "unresolved" by the pipeline's own prose will **not** trip this check — the contract gate has no visibility into inline steps at all. The ported skill's own Step 5 dispatch logic is the only thing enforcing that inline steps actually ran; `cleanup`/`cleanup-pipeline` cannot catch a skipped inline step.

Note: `ship_state{action:"gc"}` (no `-pipeline` suffix) is a different, narrower action used internally by other lifecycle paths — it is **not** the same report shape as `ship_prepare{gc:true}`, which is what the `--gc` CLI entry-mode calls (see `entry-modes.md`). Do not conflate the three.

---

## Resume

`--resume` (explicit or implicit) resolves the most recent state file for the current branch via `ship_state{action:"read"}` (or `detail.stateFile` directly, if already known). There is no derived `nextPendingStep` field in the file itself — compute the resume point by combining `ship_state{action:"next"}` (for the seven scaffolded steps) with a scan of which inline steps have a corresponding `decide` entry already recorded (for the five inline steps). A scaffolded step with status `in_progress` at resume time is retried from the beginning, matching source's behavior.
