# Plan-SDLC State File Format

The `plan` skill writes a JSON marker file to `.sdlc-v2/runs/` at the start of each planning invocation. This file records integrity checkpoints so the stop hook can verify that all quality gates were reached before the plan was presented. It also records the run's creation intent (for resuming after a context compaction), its current step checkpoint, and one row for each plan review round. A separate per-run evidence directory (`<runId>.evidence/`) holds discovery/lane/lens findings — see [Evidence Directory](#evidence-directory) below.

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
    "critiqueRan":         "2026-05-09T14:06:00.000Z",
    "done":                "2026-05-09T14:12:00.000Z"
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
      "openspecStage": false,
      "lightweight": false,
      "fileCount": 6
    }
  },
  "guardrailCounts": { "total": 3, "error": 2, "warning": 1 },
  "checkpoint": {
    "step": "3",
    "iteration": 1,
    "expectedWriters": ["lane-static-structural-r1"],
    "updatedAt": "2026-05-09T14:10:00.000Z"
  },
  "planTiming": {
    "startedAt": "2026-05-09T14:00:00Z",
    "lastModifiedAt": "2026-05-09T14:09:40Z",
    "durationMs": 580000
  },
  "guardrailResults": [
    { "id": "G14", "status": "pass", "detail": "No error-severity guardrail broken" }
  ],
  "reviewRounds": [
    { "round": 1, "mergedStatus": "Issues Found", "found": 1, "fixed": 1,
      "lenses": [{ "name": "risk", "verdict": "Issues Found" }],
      "findings": [{ "id": "f-3a9c1e07", "fixed": true }] }
  ],
  "reviewOutcome": {
    "findings": [
      { "id": "f-9d01aa42", "text": "Missing test for the stop route", "choice": "accepted", "reason": "" }
    ]
  },
  "criticalDecisions": [
    { "key": "storage", "choice": "state file", "rejected": [], "reason": "No new dependency", "at": "2026-05-09T14:11:50Z" }
  ]
}
```

| Field           | Type             | Description                                                                 |
|-----------------|------------------|-----------------------------------------------------------------------------|
| `planIntegrity` | object           | Checkpoint markers; each key's presence means that checkpoint was reached.  |
| `planFilePath`  | string \| null   | Absolute path to the written plan file. Used by the Stop hook to stat the file for non-empty content verification. `null` until the `planFile` marker is written. |
| `creationIntent`| object           | The prompt and routing decision that started this run, plus flags needed to resume it. Written once by `plan_prepare` when the run is created; never written by `plan_mark`. See [creationIntent](#creationintent) below. |
| `guardrailCounts` | object \| absent | Counts of the plan guardrails that `plan_prepare` loaded: `total`, `error`, `warning`. Written for the dashboard plan setup tile. Absent when the first call's guardrail config read failed (a later successful call without `resume` writes it). Never written for a run with no named branch. See [guardrailCounts](#guardrailcounts) below. |
| `checkpoint`    | object \| absent | The plan run's current step/iteration/expected-writers, replaced on every `plan_mark({marker: "checkpoint"})` call. Absent until the first checkpoint marker is written. See [checkpoint](#checkpoint) below. |
| `reviewRounds`  | array \| absent  | One row per Step 5 review round: `round`, `mergedStatus`, `found`, `fixed`, `lenses`, and an optional `findings`. Upserted by `plan_mark({marker: "review-round"})`. Read by the dashboard. See [reviewRounds](#reviewrounds) below. |
| `reviewOutcome` | object \| absent | The user's answer to each finding still open at the review-loop limit. Replaced on every `plan_mark({marker: "review-outcome"})` call. Absent until the first call. See [reviewOutcome](#reviewoutcome) below. |
| `planTiming` | object \| absent | `startedAt` (the `skillInvoked` time), `lastModifiedAt` (the plan file's modification time), and `durationMs` (the time between them). Rewritten by every `plan_mark` call, before its write (function `refreshPlanTiming`). The same step also rewrites `planFilePath` as an absolute, cleaned path. Left as it is when `skillInvoked` or `planFilePath` is missing, or when the plan file cannot be read. Read by ship's report step. |
| `guardrailResults` | array \| absent | One `{ id, status, detail }` entry per guardrail result. Appended to (not replaced) by every `plan_mark({marker: "guardrailResults", data: {results}})` call. Absent until the first call. The plan skill makes no such call today; the tool still accepts it. |
| `criticalDecisions` | array \| absent | One `{ key, choice, rejected, reason, at }` entry per key decision. Appended to by `plan_mark({marker: "criticalDecisions", data: {decisions}})`. Absent until the call. See [criticalDecisions](#criticaldecisions) below. |

---

## Marker Fields

Each field inside `planIntegrity` is an ISO 8601 timestamp string. Absence of a key means that checkpoint was not reached.

| Marker                | Written when                                                                      |
|-----------------------|-----------------------------------------------------------------------------------|
| `skillInvoked`        | `plan_prepare(...)` at Step 0 prepare — plan was invoked (written automatically as a side effect; no separate `plan_mark` call) |
| `planFile`            | `plan_mark({ marker: "plan-file", path: <abs> })` after Step 0 path resolution    |
| `guardrailsEvaluated` | `plan_mark({ marker: "guardrailsEvaluated" })` at end of Step 3 guardrail gate    |
| `critiqueRan`         | `plan_mark({ marker: "critiqueRan" })` as final action of Step 3                  |
| `done`                | `plan_mark({ marker: "done" })` right before the plan is presented (terminal marker). The Stop hook evaluates the other four markers only once this key is present; it is not one of the four checked markers itself. The hook does not delete the state file on `done` — see [Evaluate, Don't Delete](#evaluate-dont-delete) below. |

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
| `openspecStage`          | boolean | Whether the plan authors a new OpenSpec change and stages it (the Create-flow staging path: `plan_support`'s `openspec_instructions`/`openspec_stage` actions write into `<ACTIVE_ROOT>/.sdlc-v2/openspec-staging/<name>/`, materialized into `openspec/changes/<name>/` at the next run's start). |
| `lightweight`            | boolean | Whether the caller requested the lightweight pipeline override.      |
| `fileCount`              | integer | File count used for complexity routing.                              |

---

## guardrailCounts

Written by `plan_prepare` (function `countGuardrails`) after it loads the `plan` guardrails. The key is written for the dashboard plan setup tile. It is display data only.

When `plan_prepare` writes the key:

- A call with `resume` not `true` writes the key. The first call and a later `resolveTemplate` call both write it, so the last successful call wins.
- A `resume` call writes nothing, so the state file stays byte-identical.
- A run with no named branch has no state file, so `plan_prepare` writes nothing.
- A call whose guardrail config read fails does not write the key. It does not remove or change a key from an earlier call.

| Field     | Type    | Description                                                                 |
|-----------|---------|-------------------------------------------------------------------------------|
| `total`   | integer | Number of guardrails loaded. `0` when none are configured.                   |
| `error`   | integer | Guardrails with severity `error`. A guardrail with no severity, or a severity that is not a string, counts as `error`. |
| `warning` | integer | Guardrails with severity `warning`.                                          |

A string severity other than `error` or `warning` (for example `info` or an empty string) counts in `total` only. For this reason `error + warning` can be less than `total`. A `plan.guardrails` value that is not an array stores `{ "total": 0, "error": 0, "warning": 0 }`, because the guardrail loader reports no error for it.

The key is absent when the first call's guardrail config read failed and no later call without `resume` read it successfully (the failure is in the `errors` of the `plan_prepare` result). An absent key means no count was recorded. It does not mean zero guardrails.

---

## checkpoint

Written by `plan_mark({ marker: "checkpoint", data: {...} })`. Unlike the `planIntegrity` markers, `checkpoint` is **replaced**, not appended, on every call — it always reflects the run's current position, not its history. Absent until the first checkpoint marker is written.

| Field             | Type     | Description                                                                 |
|-------------------|----------|-------------------------------------------------------------------------------|
| `step`            | string   | The SKILL.md step this checkpoint reflects. One of `0`, `1`, `2`, `3`, `4`, `5`, `6`, `6.5`, `6.6`, `7`. |
| `iteration`       | integer  | Iteration counter (used by the Step 5/6 review loop, which can repeat up to 5 times). |
| `expectedWriters` | string[] | Writer IDs the run is currently waiting on (e.g. dispatched lane or lens subagent IDs). Max 32 entries. |
| `updatedAt`       | string   | ISO 8601 UTC timestamp stamped by `plan_mark` at write time (not caller-supplied). |

The resume flow (`plan_prepare({resume: true})`) reads `checkpoint.step` to tell the orchestrator where to pick back up, and `checkpoint.expectedWriters` together with `plan_support({action: "evidence_digest"})` to tell it which writers it is still waiting on.

---

## reviewRounds

Written by `plan_mark({ marker: "review-round", data: {...} })`, once for each Step 5 review round, at every exit of the round (Step 5 Approved, or Step 6 after the fixes; the last round also records in Step 6, before it goes to the user). `reviewRounds` lives in its own top-level state key and never participates in the Stop hook's four-marker check. Unlike `criticalDecisions`, it is **not appended to**: each call **upserts** one row by `round`. A call replaces the row with the same `round`, or inserts a new row. A resumed run can send the same round twice, so a replay is safe. The list stays sorted by `round`. The key is absent until the first `review-round` call. The dashboard reads the rows. They are display data only.

Caps: a state file holds at most **20 rounds**. Each round holds at most **32 lenses** and **200 findings**. A call over a cap returns an error and leaves the state file unchanged.

Each row of `reviewRounds` (the first five keys are required, `findings` is optional, and no other key is allowed):

| Field          | Type     | Description                                                                 |
|----------------|----------|-------------------------------------------------------------------------------|
| `round`        | integer  | The Step 5 iteration counter after its increment: the number of completed rounds, including this one (>= 1). Not the in-progress `<iteration>` of the checkpoint. |
| `mergedStatus` | string   | The `mergedStatus` of the round's `merge_results` call. Exactly `Approved` or `Issues Found`. |
| `found`        | integer  | Blocking issues found in the round (>= 0): the `blockingCount` of the `merge_results` call. |
| `fixed`        | integer  | Blocking issues the round fixed (>= 0). `0` for an Approved round. The last round (`reviewLoop.maxRounds`) also gets the Step 6 fix pass, so its `fixed` is the real count. |
| `lenses`       | object[] | One `{ name, verdict }` for each lens (max 32). `name`: letters, digits, `.`, `_`, `-` (max 64); the first character must be a letter or a digit (`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, the same rule as `writerId`). `verdict`: exactly `Approved` or `Issues Found`. A single reviewer (<5 tasks) gives `[{ "name": "all", "verdict": "<mergedStatus>" }]`. Lanes are not listed. |
| `findings`     | object[] \| absent | One `{ id, fixed }` for each finding of the round (max 200). `id`: the `id` of the issue in the `merge_results` `allIssues`, `f-` plus 8 lower-case hex characters (`^f-[0-9a-f]{8}$`). `fixed`: a boolean, `true` when the round fixed the finding. Each `id` appears once in a call: a repeated `id` returns an error. Guardrail findings and lens findings share one ID space, so no kind is stored. A passed `[]` is stored as `[]`. The key is absent when the call did not send `findings` (an older caller). An invalid entry returns an error and writes nothing. |

```json
{
  "reviewRounds": [
    {
      "round": 1,
      "mergedStatus": "Issues Found",
      "found": 2,
      "fixed": 2,
      "lenses": [
        { "name": "architecture", "verdict": "Approved" },
        { "name": "requirements", "verdict": "Issues Found" },
        { "name": "risk", "verdict": "Issues Found" }
      ],
      "findings": [
        { "id": "f-3a9c1e07", "fixed": true },
        { "id": "f-0b77d2c4", "fixed": true }
      ]
    },
    {
      "round": 2,
      "mergedStatus": "Approved",
      "found": 0,
      "fixed": 0,
      "lenses": [
        { "name": "architecture", "verdict": "Approved" },
        { "name": "requirements", "verdict": "Approved" },
        { "name": "risk", "verdict": "Approved" }
      ]
    }
  ]
}
```

---

## reviewOutcome

Written by `plan_mark({ marker: "review-outcome", data: { findings: [...] } })` after the review loop reaches its limit with findings still open, and the user answers each one. Like `checkpoint`, `reviewOutcome` is **replaced**, not appended, on every call: the stored list is the list of the last call. Pass every answered finding on every call. For example, call 1 with `a` and `b`, then call 2 with `a` alone, leaves only `a`. The key is absent until the first call. A call with invalid data returns an error and leaves the stored outcome unchanged.

The call returns one of two `next` texts. The tool picks the text from the stored choices:

| Choices in the call | `next` |
|---|---|
| No choice is `stop` | "Outcome stored. No choice is stop: run Create-flow authoring (Create flow only), then Step 6.5." |
| At least one choice is `stop` | "Outcome stored. A choice is stop: offer harden when interactive, then end the run and report the open findings. Do not hand off the plan." |

`findings` holds 1 to 200 entries. Each entry has all 4 keys, each a string, and no other key:

| Field    | Type   | Description                                                                 |
|----------|--------|-------------------------------------------------------------------------------|
| `id`     | string | The `id` of the issue in the `merge_results` `allIssues` (`^f-[0-9a-f]{8}$`). |
| `text`   | string | The finding text that the user saw. Max 200 characters (runes).             |
| `choice` | string | Exactly `accepted`, `rejected`, or `stop`.                                  |
| `reason` | string | The user's reason. Max 200 characters (runes). `""` when there is none.     |

Rejected input (each returns an error with a suggestion, and writes nothing):

| Input | Suggestion |
|---|---|
| `choice` not `accepted`, `rejected`, or `stop` | Use accepted, rejected, or stop. |
| `id` does not match `^f-[0-9a-f]{8}$` | Pass the id from merge_results allIssues. |
| More than 200 findings | Stop and report the open findings. Do not call review-outcome. |
| `text` or `reason` over 200 characters | Shorten the text to 200 characters. |
| `findings` missing, `null`, or empty | Pass at least one answered finding. The stored outcome is unchanged. |
| `findings` is not an array (for example a string or an object) | Pass findings as a JSON array, not a string or an object: … The stored outcome is unchanged. |
| The same `id` in two entries | Pass each finding id once. Remove the second "<id>" entry. |
| An entry without a string `id`, `text`, `choice`, or `reason` | Give all 4 fields for each finding. Use an empty reason only as "". |

An unknown key in `data` or in an entry is also rejected.

```json
{
  "reviewOutcome": {
    "findings": [
      { "id": "f-9d01aa42", "text": "Missing test for the stop route", "choice": "accepted", "reason": "Covered by the flow walk task" }
    ]
  }
}
```

---

## criticalDecisions

Written by `plan_mark({ marker: "criticalDecisions", data: { decisions: [...] } })`, exactly once per run, right before the terminal `done` marker. Like `guardrailResults` (`plan_mark({ marker: "guardrailResults", data: { results: [...] } })`, shape `{id, status, detail}` per entry), `criticalDecisions` is a **structured-data marker**: it lives in its own top-level state key, is **appended to**, not replaced, on every call, and never participates in the Stop hook's four-marker check.

Each entry of the `decisions` array:

| Field      | Type     | Description                                                                 |
|------------|----------|-------------------------------------------------------------------------------|
| `key`      | string   | Short identifier for the decision.                                          |
| `choice`   | string   | What was chosen.                                                             |
| `rejected` | object[] | Alternatives considered: `[{ option: string, why: string }]`. Defaults to `[]` when the caller omits it. |
| `reason`   | string   | Why the chosen option won.                                                   |
| `at`       | string   | ISO 8601 UTC timestamp. Always stamped by `plan_mark` at call time — any caller-supplied `at` is overwritten. |

```json
{
  "criticalDecisions": [
    {
      "key": "auth-token-storage",
      "choice": "httpOnly cookie",
      "rejected": [
        { "option": "localStorage", "why": "vulnerable to XSS" }
      ],
      "reason": "cookie survives a reload without exposing the token to JS",
      "at": "2026-05-09T14:06:30.000Z"
    }
  ]
}
```

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
| `writerId`  | string | The writer's own ID. Must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` (max 64 chars). `"main"` is reserved for the orchestrator session. |
| `status`    | string | `running` or `done`.                                                              |
| `updatedAt` | string | ISO 8601 UTC timestamp of the last `evidence_record` call for this writer.        |
| `items`     | array  | Up to 200 items, max 64 KiB total file size. Each item: `id` (unique per writer, e.g. `F-auth-1`; must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`), `summary` (one line, max 200 chars), `ref` (optional, one line, max 500 chars — `path:line` or URL), `body` (optional Markdown, returned only by `evidence_get`). |

Writer files are capped at 32 per run (`evidenceMaxWriters`). `evidence_digest` reads every writer file's `status`/`updatedAt`/item summaries to report which writers are still `running`, which have gone stale (`stalledWriters`), and which expected writers never wrote a file at all (`missingWriters`). A writer file that exists but cannot be parsed is listed in `unreadableWriters`; when that writer is expected it is also listed in `stalledWriters`, because it can never report `done`. All three lists sit under the output's `writers` object. The next `evidence_record` call for that writer replaces the unreadable file.

---

## Lifecycle Rules

### Write

1. On a genuinely new run (no active run for the branch, and not a `resume: true` call), `plan_prepare(...)` writes the new marker atomically with `planIntegrity: { skillInvoked: <ISO-ts> }` and `creationIntent: { userPrompt, timestamp }` through `state.Write`, which prunes prior `plan-<branchSlug>-*.json` files for the same branch — **except** a sibling run whose `planIntegrity.done` marker is already set, which is kept (see [Evaluate, Don't Delete](#evaluate-dont-delete) below). It then best-effort prunes stale `<runId>.evidence/` directories left by earlier runs the same way (`state.PruneEvidenceDirs`, same done-run exception). When the guardrail config read succeeds, the same call then writes the state file a second time, to add `guardrailCounts` (see [guardrailCounts](#guardrailcounts)).
2. The first `resolveTemplate: true` call for that run overwrites `creationIntent` with the full shape (`fullCreationIntent`: userPrompt, scope, routing, timestamp, flags). Every `plan_prepare` call without `resume: true` (the first call and the `resolveTemplate` call) also writes `guardrailCounts` again, in a second `state.Write`, when the guardrail config read succeeds.
3. A `resume: true` call does not create or prune anything, and writes nothing (not even `guardrailCounts`); it reads the branch's active run back (`state.ActivePlanRun`) and restores `creationIntent` into the caller's input (`applySavedIntent`) instead of overwriting it.
4. Subsequent `plan_mark({ marker, path })` calls update the `planIntegrity` keys and `planFilePath` in-place, atomically; `plan_mark({ marker: "checkpoint", data })` replaces `checkpoint` in-place, atomically; `plan_mark({ marker: "review-round", data })` upserts one `reviewRounds` row by `round`, atomically; `plan_mark({ marker: "review-outcome", data })` replaces `reviewOutcome` in-place, atomically; `plan_mark({ marker: "guardrailResults", data })` and `plan_mark({ marker: "criticalDecisions", data })` append their array payload to their own top-level key (`guardrailResults`, `criticalDecisions`). Before each of these writes, every `plan_mark` call also refreshes `planTiming` and rewrites `planFilePath` as an absolute, cleaned path (`refreshPlanTiming`; skipped when `skillInvoked` or `planFilePath` is missing, or the plan file cannot be read). Every `plan_mark` write goes through `state.Write`, with the same done-run exception as step 1 — so a finished (`done`) run can survive alongside a newer in-progress run for the same branch until it is removed (see below).

### Evaluate, Don't Delete

The stop hook runs at session end:

1. Calls `findStateFile('plan', branchSlug)` and captures the returned path.
2. Reads the marker via `readState`.
3. If the `done` marker is absent, the plan is still running (e.g. a Stop fired mid-plan across a compaction): returns silently, with no evaluation and no deletion.
4. Once `done` is present: evaluates the four required `planIntegrity` keys (`skillInvoked`, `planFile`, `guardrailsEvaluated`, `critiqueRan`) and stats `planFilePath`; any missing or failing marker produces one aggregated warning, never a block.
5. The state file and its per-run evidence directory (`state.EvidenceDir(st.Root, state.RunID(st))`) are left on disk — **this hook no longer deletes them.** A finished run survives so ship's `report` step can later read `planIntegrity`/`planTiming` for the ship report's `## Planning`/`## Timeline` sections. Removal happens afterward, via `ship_state({action: "cleanup-pipeline"})` or the GC TTL sweep below — see `docs/plan-architecture.md`'s **Plan run lifetime** table for the full path.
6. Because the file is no longer deleted on `done`, a later Stop event on the same branch finds the same state file and simply re-evaluates (silently, if nothing is missing). The transcript-fallback path (R21) engages only when `findStateFile` finds no plan state file for the branch at all.

Every step above is wrapped so a read or stat failure cannot break the hook's advisory-only exit-0 contract.

### GC Orphan Sweep

Stale plan markers (abandoned sessions, branch-deleted, TTL-expired) are removed by `ship --gc` and `execute --gc` via the same TTL+branch-liveness sweep (`state.GC`, `{ prefix: 'plan', ttlDays, knownBranches }`). For a live branch, `state.GC` keeps a file that is within the TTL **or** is the newest of its branch; it deletes a file only once it is both past the TTL **and** no longer the newest — a gone branch's files are deleted regardless of age. `ship_state({action: "cleanup-pipeline"})` runs this same sweep, plus its own report-gated removal of a `done` plan run once the ship report has read it (see [Evaluate, Don't Delete](#evaluate-dont-delete) above and `docs/plan-architecture.md`'s **Plan run lifetime** table; the Stop hook itself never deletes anything). The sweep reports plan-prefix files in a `plan` bucket alongside the existing `ship` and `execute` buckets in the JSON output. `execute_state` gc (and `cleanup-pipeline`) also reap `runs/` subdirectories older than the TTL that do not belong to a live execute run (`execReapRunDirectories` in `internal/tools/execute_state.go`); this includes abandoned `<runId>.evidence/` directories, independently of whether the matching `.json` state file has been removed yet. Evidence directories are therefore cleaned by three paths: `state.PruneEvidenceDirs` (on the next new run for the same branch, for any sibling run that is not itself a done plan run), `cleanup-pipeline`'s report-gated removal, and this TTL reap.

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
    "critiqueRan":         "2026-05-09T14:06:01.012Z",
    "done":                "2026-05-09T14:12:40.345Z"
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
      "openspecStage": false,
      "lightweight": false,
      "fileCount": 6
    }
  },
  "guardrailCounts": { "total": 3, "error": 2, "warning": 1 },
  "checkpoint": {
    "step": "5",
    "iteration": 2,
    "expectedWriters": ["lens-architecture-r1", "lens-requirements-r1", "lens-risk-r1"],
    "updatedAt": "2026-05-09T14:06:30.000Z"
  }
}
```

A marker file with only `skillInvoked`, the bootstrap `creationIntent`, and `guardrailCounts` set (plan was invoked but crashed before writing the plan file):

```json
{
  "planIntegrity": {
    "skillInvoked": "2026-05-09T14:00:05.123Z"
  },
  "planFilePath": null,
  "creationIntent": {
    "userPrompt": "Fix the auth redirect bug",
    "timestamp": "2026-05-09T14:00:05.123Z"
  },
  "guardrailCounts": { "total": 3, "error": 2, "warning": 1 }
}
```

The Stop hook would report `planFile`, `guardrailsEvaluated`, and `critiqueRan` as missing checkpoints.
