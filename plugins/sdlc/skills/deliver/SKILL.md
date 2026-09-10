---
name: deliver
description: "Use this skill to drive the full delivery pipeline unattended for hours: execute a plan, review the result, fix findings in a loop, and ship — without stopping between phases. Orchestrates execute, review, and ship as Agent-dispatched sub-skills (ship's own verify-pipeline step handles post-PR CI verification inline; deliver never dispatches verify-pipeline itself). Manages its own resumable phase/iteration state file directly via Read/Write — zero new MCP tools. Arguments: <plan-file-path> [--resume] [--quality full|balanced|minimal] [--rebase auto|skip] [--bump patch|minor|major|<label>] [--draft] [--dry-run]. Triggers on: deliver this, run the full pipeline unattended, ship end to end without stopping, deliver."
user-invocable: true
argument-hint: "<plan-file-path> [--resume] [--quality full|balanced|minimal] [--rebase auto|skip] [--bump patch|minor|major] [--draft] [--dry-run]"
model: sonnet
---

# Deliver (SDLC)

Drive plan execution, review, a fix loop, and shipping end to end, unattended, by composing three already-ported skills as black-box Agent dispatches: execute, review, and ship. This skill introduces **zero new MCP tools** — it composes the existing surface and manages its own plain-JSON phase/iteration state file directly via the Read/Write built-in tools.

**Announce at start:** "I'm using deliver (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

Unlike ship, deliver has **no `--auto` flag**. Unattended operation is the entire point of this driver: every dispatched sub-skill is always instructed to run in its own auto mode. There is nothing to opt into.

---

## Plan Mode Check

If the system context contains "Plan mode is active":

1. Announce: "This skill requires write operations (file edits, shell commands, agent dispatches). Exit plan mode first, then re-invoke `/deliver`."
2. Stop. Do not proceed to subsequent steps.

---

## Step 0: Prerequisites

Parse `$ARGUMENTS`: a positional plan-file path, `--resume`, `--quality full|balanced|minimal`, `--rebase auto|skip`, `--bump patch|minor|major|<label>`, `--draft`, `--dry-run`.

Resolve the current branch: `git branch --show-current`. Slugify it exactly like `internal/state`'s `SlugifyBranch` — replace every character that is not alphanumeric or `-` with `-`.

**Plan-argument gate:** if no positional plan path was supplied and `--resume` was not passed, stop:

> deliver cannot run without an explicit plan document.
> Fix: `/deliver <path-to-plan.md>`. If resuming an existing run, use `/deliver --resume`.

Do not guess a plan path from conversation context, even one just discussed in this session.

**`--dry-run`:** read `.sdlc-v2/local.json` (Step "fix-loop" below explains exactly which fields), print the six-phase sequence (Plan / Execute / Review / Fix-loop / Verify-pipeline / Ship) and the resolved `reviewFixIterations` / `reviewFixSeverityThreshold` / ship `--steps` list this run would use, and stop. Do not create a state file, do not dispatch anything.

---

## Step 1 (INIT/RESUME): Own state file

deliver's state lives entirely outside `ship_state`/`execute_state` — neither tool exposes a "kind" for a pipeline-of-pipelines, and the plan's Contract line forbids a new MCP tool for it. This step manages a plain JSON file directly with Read/Write, mirroring (but never touching) `internal/state`'s `<prefix>-<branchSlug>-<timestamp>.json` naming convention.

**Path grammar:** `.sdlc-v2/execution/deliver-<branch-slug>-<timestamp>.json`, where `<timestamp>` is `time.Now().UTC()` formatted `20060102T150405Z` (e.g. `20260906T091500Z`) — the exact format `internal/state/state.go`'s `Init` uses. The `deliver` prefix is intentionally invisible to `internal/state`'s own filename parser (which only accepts `ship|execute|plan|commit`) and to that package's GC — this file's entire lifecycle is owned by this skill's own prose, never by `state.Init`/`state.Find`/`state.Write`/`state.GC`.

**Fresh run (no `--resume`):**

1. Glob `.sdlc-v2/execution/deliver-<branch-slug>-*.json`. Delete any matches (best-effort) — mirrors `state.Write`'s prune-on-write behavior; a leftover file here can only be a stale artifact of an earlier, already-terminal run on the same branch.
2. Create the new file at a freshly timestamped path with:
   ```json
   {
     "branch": "<branch>",
     "planPath": "<resolved plan path>",
     "flags": {"quality": "...", "rebase": "...", "bump": "...", "draft": false},
     "phase": "plan",
     "phaseHistory": [],
     "fixLoop": {"iteration": 0, "maxIterations": 3, "severityThreshold": "high", "history": []},
     "terminal": null,
     "createdAt": "<ISO8601>",
     "updatedAt": "<ISO8601>"
   }
   ```
3. Every phase transition below **overwrites this same path in place** (same filename — never a new timestamp mid-run, never a re-prune).

**Resume (`--resume`):**

1. Glob `.sdlc-v2/execution/deliver-<branch-slug>-*.json`. If none found, stop:
   > No deliver run found for branch "<branch>". Fix: start a fresh run with `/deliver <path-to-plan.md>`.
2. Read the most recently modified match. Its `phase` field says which of the six phases below to re-enter; its `planPath` field is used instead of requiring a fresh positional argument.
3. If `terminal` is already `"delivered"`, report that the run already completed successfully and stop — do not re-dispatch anything.
4. If `terminal` is `"failed(<step>)"`, this is **not necessarily a hard error** — see the Ship phase's manual-gate note below. Re-dispatch the sub-skill whose phase the state names, exactly as if resuming from that phase fresh. deliver has no phase-specific resume special-casing beyond this: it always just re-enters `phase` and re-dispatches (or re-evaluates) that phase's own step.

---

## Step 2 (DO): Dispatch phases

### Dispatch protocol

Every Agent-dispatched sub-skill (execute, review, ship) uses this exact prompt shape, verbatim from ship's own idiom, and never `isolation: "worktree"`:

```
You are executing the <skill-name> skill. Invoke `/<skill-name> <args>` using the Skill tool — this loads the SKILL.md automatically. Return a structured result:
(1) status — success or failure
(2) result summary — 2-3 lines
(3) artifacts — commit hash, tag, PR URL, verdict, etc.
(4) any warnings or issues encountered
```

Each dispatched skill is a black box — never call a dispatched skill's own MCP tools directly (never call `ship_state`/`execute_state`'s step-shaped actions, `review_prepare`, `ship_prepare`, etc. from deliver itself; only the dispatched Agent calls those). Per-dispatch additions to this base template (below, for review and ship) follow skill-specific reporting idioms — augmenting the template with such instructions is in-idiom, not a deviation from it.

### plan

No sub-skill dispatch. "Plan" is intake, not execution: this step already ran in Step 0/1 (the positional plan path was validated and recorded as `planPath` in the state file). Advance directly to `execute`. Record the transition:

Update state: `phase: "execute"`, append `"plan"` to `phaseHistory`, `updatedAt: <now>`. Write the file.

### execute

Dispatch execute:

```
/execute <planPath> [--quality <quality>] [--rebase <rebase>]
```

execute always runs its own internal auto mode for wave dispatch; do not forward a `--auto` flag (its meaning there is unrelated to deliver's own unattended posture — see ship's own execute step for the same caution). execute's Step 9 (REPORT) result is **free text** (a fixed-width summary block), not JSON — translate it into the 4-part structured result yourself in this dispatch's own returned message; do not expect a JSON payload back.

After the dispatch returns, run `execute_state({action:"verify-completeness"})` exactly as ship does after its own execute step — this is a legitimate direct call (a read-only wave/task-shaped sanity check on execute's own state, not a step-shaped action on deliver's behalf). On `{ok:true, ...}`, continue. On a `DataError` reporting missing task IDs, treat it as a hard failure.

Stage the working tree for the commit ship's own `commit` step will make later — execute creates and modifies files but never stages them:

```bash
git add -A -- ':!.sdlc-v2/'
```

**Failure:** dispatch failure, or a `verify-completeness` mismatch → `terminal: "failed(execute:dispatch-failure)"` or `"failed(execute:incomplete)"`. Write state, stop. Resume re-dispatches execute with `--resume --plan <planPath>` — its own resume path reads its execute-state file and continues from the recorded wave.

**Success:** `phase: "review"`, append `"execute"` to `phaseHistory`. Write state.

### review

Dispatch review with these additions on top of the base template:

```
/review

In addition, for this deliver dispatch:
- At Step 7 (Handle Posting), always choose "save" — never "yes"/post, never "cancel" — regardless of
  whether a PR exists yet (none does, at this point in the pipeline).
- At Step 8 (Offer Self-Fix), always answer "no" — deliver's own fix loop applies fixes, not
  review's self-fix path.
- In your artifacts section, report exactly these three lines, verbatim, each on its own line:
    verdict: <CHANGES REQUESTED|APPROVED WITH NOTES|APPROVED>
    severity-counts: critical=<N> high=<N> medium=<N> low=<N> info=<N>
    saved-review: <path written by Step 7's save option>
```

**Port note (resolves a tension the plan text did not anticipate):** review's own Step 9 (Cleanup) unconditionally deletes its ledger directory and `diff_dir` on every terminal path, including normal completion — and it does this *before* this Agent dispatch returns control here. The `.sdlc-v2/execution/ledger/{runId}/{workerId}.findings.json` files a naive reading might expect to re-read "after dispatching review" no longer exist by then. The `saved-review` path from Step 7's "save" option is the only durable artifact review leaves behind (`.sdlc-v2/reviews/<branch>-<date>.md`, untouched by Step 9) — so this dispatch is instructed to always choose "save" and to report that path back in artifacts. This still satisfies the substance of "no file-scoped re-review, only full re-dispatch": every fix-loop iteration below re-dispatches review fully, exactly as required; only the mechanism for reading its output changed from a since-deleted ledger file to a durable saved-review file.

Parse `verdict`, `severity-counts`, and `saved-review` from the returned artifacts by line prefix (not JSON — this is deliver's own translation of a sub-skill's free-form artifacts text, same idiom as the execute dispatch above). **Fail closed** on an unparseable or incomplete result — do not default a missing verdict to APPROVED WITH NOTES the way ship's own lenient text-detection does; an unattended driver silently skipping the fix loop on a parse failure is worse than stopping.

**Failure:** dispatch failure, or any of the three lines missing/unparseable → `terminal: "failed(review:unparseable-result)"`. Write state, stop. Resume re-dispatches review fresh — review has no `--resume` of its own and Finding 4 rules out any file-scoped re-review, so "resuming" this phase always means a full fresh dispatch.

**Success:** record the parsed `verdict`, `severity-counts`, and `saved-review` as the fix loop's iteration-0 result (`fixLoop.history[0]`). `phase: "fix-loop"`, append `"review"` to `phaseHistory`. Write state.

### fix-loop

Read `.sdlc-v2/local.json` directly with the Read tool (Finding 5 — `StepMode` is ship-internal and does not accept caller-supplied step names). Under the top-level `automation` key:

- `reviewFixIterations` (default `3` when the file, the `automation` section, or the field is absent)
- `reviewFixSeverityThreshold` (default `"high"` under the same absence rule)

Rank severities exactly like `internal/tools/review.go`'s `severityRank`: `critical=5, high=4, medium=3, low=2, info=1`. A review result's findings **meet the threshold** when any severity with a non-zero count has a rank ≥ the configured `reviewFixSeverityThreshold`'s rank.

Loop, starting from the `review` phase's iteration-0 result already in `fixLoop.history`:

1. If the current result does not meet the threshold → the fix loop is done with nothing to fix. `phase: "verify-pipeline"`, append `"fix-loop"` to `phaseHistory`. Write state. Exit the loop.
2. If it meets the threshold and `fixLoop.iteration >= reviewFixIterations` → the loop is exhausted without a clean result. `terminal: "failed(fix-loop:threshold-exceeded)"`. Write state (`fixLoop.history` keeps every iteration's `saved-review` path for a human to inspect). Stop.
3. Otherwise, dispatch **one** sequential fix-implementer Agent (a plain `Agent` tool dispatch, not a `/skill` invocation — this worker has no corresponding skill file):
   ```
   You are a fix-implementer for deliver's fix loop (iteration <fixLoop.iteration + 1> of <reviewFixIterations>).
   Read the review findings saved at <saved-review path from the current result>.
   Apply fixes for every finding at or above severity "<reviewFixSeverityThreshold>". Findings below
   that threshold are informational — leave them unless fixing a higher-severity finding requires
   touching the same code.
   Edit files directly. Do NOT run `git commit`, `git push`, or `git tag` — deliver's ship phase
   commits later.
   Return a structured result:
   (1) status — success or failure
   (2) result summary — 2-3 lines describing what was fixed
   (3) artifacts — list of files changed
   (4) any warnings or issues encountered (e.g. a finding that could not be safely fixed)
   ```
   A single sequential worker per iteration, reading the consolidated findings and applying all fixes itself in one dispatch — no fan-out, no ledger, no coordination machinery beyond this one Agent call (Q3). If the worker itself reports failure, `terminal: "failed(fix-loop:worker-failure)"`, stop.
4. Re-stage: `git add -A -- ':!.sdlc-v2/'`.
5. Re-dispatch review **fully**, using the exact same augmented dispatch shape as the `review` phase above (save at Step 7, decline at Step 8, pinned three-line artifacts) — no file-scoped re-review, per Finding 4.
6. Increment `fixLoop.iteration`, append the new iteration's `{iteration, verdict, severityCounts, savedReviewPath}` to `fixLoop.history`. Write state. Go to step 1 with this new result as "the current result."

### verify-pipeline

No standalone dispatch of verify-pipeline here — that skill is a one-shot classify/fix tool that requires an existing PR (`--pr <N>` or `--logs`) and is itself dispatched by ship's own `verify-pipeline` step, never before a PR exists. This phase is a pass-through: `phase: "ship"`, append `"verify-pipeline"` to `phaseHistory`. Write state. Post-PR CI verification happens inside the `ship` phase's own dispatch, by including `verify-pipeline` in the `--steps` list passed to ship below.

### ship

Dispatch ship with `execute` and `review` excluded from its step list — deliver already performed both directly, and letting ship re-run either would re-execute the plan or re-review the same diff a second time:

```
/ship --steps commit,archive-openspec,pr,verify-pipeline,learnings-commit [--bump <bump>] [--draft] [--resume if this phase was already entered once before]
```

If the project's `.sdlc-v2/local.json` configures `ship.steps` explicitly, use that list with `execute` and `review` removed instead of the fixed list above, so project-level customization (e.g. adding `verify-openspec` or `await-remote-review`) still applies.

**Scaffold guard (required — prevents a hard crash):** `ship_state`'s init always scaffolds the fixed 6-step list (`execute, commit, review, received-review, commit-fixes, pr`) regardless of `--steps` — `plan`/`archive-openspec`/`verify-pipeline`/`learnings-commit` are separate inline steps outside this scaffold, not part of it. The `execute`/`review` scaffold entries carry no `condition` key — so left `pending` they permanently block every later step's `begin-step` call (`received-review`/`commit-fixes` already carry a `condition` key and don't need skipping). ship's own Step 5 has no "skip if absent from `flags.steps`" guard for `execute`/`review` (unlike `verify-pipeline`/`await-remote-review`/`learnings-commit`, which do have one). Add this instruction to the ship dispatch, beyond the base template, so the dispatched Agent neutralizes the scaffold itself instead of hitting the crash:

```
Before beginning your first real step (`commit`), call:
  ship_state({action:"skip", step:"execute", detail:{reason:"performed directly by deliver"}})
  ship_state({action:"skip", step:"review", detail:{reason:"performed directly by deliver"}})
Do this once per fresh run (not on --resume, where these steps are already
skipped from the prior attempt). Skipping is idempotent and safe to call again if unsure.
```

**Manual-gate handling (Q2 — the least source-precedented part of this skill):** ship has one hardcoded manual `AskUserQuestion` gate that survives even `automation.mode: unattended` — the post-PR-commit push pause (after its `verify-pipeline` auto-fix commit or its `learnings-commit`). No tool in the tool registry can push a branch, so this gate is permanent, not a bug. Add this instruction to the ship dispatch, beyond the base template:

```
If you reach your manual AskUserQuestion gate (a push pause after `verify-pipeline` or
`learnings-commit`) and no interactive human is available to answer it, do not wait indefinitely.
Stop immediately after surfacing the gate and return:
(1) status: "blocked"
(2) result summary naming exactly which gate was reached
(3) artifacts containing the exact pending action (the branch to push)
(4) no warnings beyond the above
```

When the ship dispatch returns `status: "blocked"`, treat it as `terminal: "failed(ship:manual-gate-pending)"`. **This is not a hard error — it is deliver correctly waiting on a human.** Write the pending action from artifacts into the state file's `terminal` detail so a human resuming later sees exactly what to run. Resuming after the human completes the manual step is just `/deliver --resume`: this re-dispatches ship with `--resume`, and ship's own Step 1c reads its own `ship_state` and continues past the now-completed step on its own — deliver needs no gate-specific resume logic beyond its normal "re-dispatch the sub-skill whose phase we were in" path.

**Failure:** any other dispatch failure → `terminal: "failed(ship:dispatch-failure)"`. Write state, stop. Resume re-dispatches ship with `--resume`.

**Success:** `terminal: "delivered"`, append `"ship"` to `phaseHistory`, `phase: "terminal"`. Write state.

---

## Step 3 (TERMINAL): State contract

### terminal

Every exit path from this skill — success or failure, on every phase — writes one of exactly two values to the state file's `terminal` field before this skill's own turn ends:

- `"delivered"` — the entire pipeline completed: executed, reviewed, fixed until clean (or already clean), and shipped.
- `"failed(<step>)"` — the run stopped at `<step>` (one of `plan`, `execute`, `review`, `fix-loop`, `ship`, optionally suffixed `:<reason>`, e.g. `failed(ship:manual-gate-pending)`).

No third bucket exists. `failed(ship:manual-gate-pending)` is the one value in this contract that is not actually an error — see the `ship` phase note above — but it is still recorded as `failed(<step>)`, not a separate `paused(...)` state, per this contract's strict two-value shape.

Print a final summary naming the terminal value, the state file path, and — on any `failed(<step>)` — the exact resume command (`/deliver --resume`) and, for `ship:manual-gate-pending` specifically, the pending manual action recorded in state.

---

## Failure policy (step table)

| Step | Dispatch / tool call | Success transition | Failure policy |
|------|----------------------|---------------------|-----------------|
| plan | (intake only — no dispatch) | → execute | `failed(plan:no-plan-file)` if no plan path is resolvable at Step 0. Not resumable in place — re-run with a valid path. |
| execute | Agent dispatch `execute`; `execute_state({action:"verify-completeness"})` | → review | `failed(execute:dispatch-failure)` / `failed(execute:incomplete)`. Resume: re-dispatch execute `--resume`. |
| review | Agent dispatch `review` | → fix-loop | `failed(review:unparseable-result)`. Resume: re-dispatch review fresh (no file-scoped resume). |
| fix-loop | Agent fix-implementer (per iteration) + Agent dispatch `review` (re-check) | → verify-pipeline (once below `reviewFixSeverityThreshold`) | `failed(fix-loop:threshold-exceeded)` after `reviewFixIterations` exhausted; `failed(fix-loop:worker-failure)` if a fix-implementer dispatch fails. Resume: re-enters the loop at the recorded `fixLoop.iteration`. |
| verify-pipeline | (pass-through — delegated into the ship dispatch's own `--steps`) | → ship | n/a — this phase cannot itself fail; a CI failure surfaces inside the `ship` phase's own dispatch. |
| ship | Agent dispatch `ship --steps commit,archive-openspec,pr,verify-pipeline,learnings-commit` | → terminal `delivered` | `failed(ship:manual-gate-pending)` — **not a hard error**, correctly waiting on a human; resume with `/deliver --resume`. `failed(ship:dispatch-failure)` — a genuine error; resume re-dispatches ship `--resume`. |
| terminal | Write state file's `terminal` field | (run ends) | n/a |

---

## DO NOT

- Do NOT call any dispatched sub-skill's own MCP tools directly (`ship_state`, `execute_state`'s step-shaped actions — which do not exist —, `review_prepare`, `ship_prepare`, `pr_apply`, `commit_apply`, etc.). Every sub-skill is a black box; only the dispatched Agent calls those.
- Do NOT reference `execute_state`'s `begin-step`/`complete-step` — that vocabulary belongs only to `ship_state`. `execute_state`'s 19 actions are all wave/task/ledger-shaped nouns (`verify-completeness` is the one legitimate direct call this skill makes).
- Do NOT invent a third terminal-state bucket (no `paused(...)`). The contract is strictly `delivered` or `failed(<step>)`.
- Do NOT expect JSON back from execute's Step 9 report — it is free text; translate it yourself.
- Do NOT re-read `.sdlc-v2/execution/ledger/{runId}/{workerId}.findings.json` after a review dispatch returns — review's own Step 9 has already deleted it. Use the `saved-review` path reported in artifacts instead.
- Do NOT fan out multiple fix-implementer Agents per iteration, and do NOT build a findings ledger for the fix loop — one sequential Agent per iteration, reading the saved review and applying every fix itself (Q3).
- Do NOT dispatch verify-pipeline standalone — it requires an existing PR and is already dispatched by ship's own `verify-pipeline` step.
- Do NOT run `git commit`, `git push`, or `git tag` directly from deliver's own prose (only `git add` for staging, mirroring ship's own inline staging step) — every commit/tag/push routes through a dispatched sub-skill's executor tools or its own human-facing pause.
- Do NOT let deliver's own state file be discovered or mutated by `internal/state`'s `Init`/`Find`/`Write`/`GC` — the `deliver` prefix is deliberately outside that package's filename grammar.

---

## See Also

- [`/execute`](../execute/SKILL.md) — dispatched for the `execute` phase.
- [`/review`](../review/SKILL.md) — dispatched for the `review` phase and every `fix-loop` re-check.
- [`/ship`](../ship/SKILL.md) — dispatched for the `ship` phase; owns `pr`, `verify-pipeline`, and `learnings-commit`, and the two manual gates documented above.
