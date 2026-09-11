---
name: ship
description: "Use this skill to run the full ship pipeline: execute a plan, commit, review, open a PR, and optionally verify CI and await an automated reviewer. Orchestrates execute, commit, review, received-review, pr, and verify-pipeline as Agent-dispatched sub-skills, and runs a handful of steps (rebase, OpenSpec validate/archive, CI-poll, remote-review-poll, learnings commit) inline. Arguments: [--auto] [--steps <csv>] [--quick] [--quality full|balanced|minimal] [--bump patch|minor|major|<label>] [--draft] [--dry-run] [--resume] [--plan <path>] [--openspec-change <name>] [--gc] [--ttl-days <N>] [--init-config]. Triggers on: ship it, ship this, run the ship pipeline, full release pipeline, execute plan then release, ship."
user-invocable: true
argument-hint: "[--auto] [--steps <csv>] [--quick] [--quality full|balanced|minimal] [--bump patch|minor|major] [--draft] [--dry-run] [--resume] [--plan <path>] [--gc] [--init-config]"
model: sonnet
---

# Ship Pipeline (SDLC)

`execute → commit → review → (received-review) → (commit-fixes) → [rebase] → (verify-openspec) → (archive-openspec) → pr → (verify-pipeline) → (await-remote-review) → learnings-commit → cleanup`. This skill is a thin stepper loop over `ship_prepare`/`ship_state` — those tools own progress tracking, narration, and resume detection; this file only sequences calls and makes the judgment calls the tools can't.

**Announce at start:** "I'm using ship (sdlc v{sdlc_version})." (version from the session-start system-reminder's `sdlc:` line; omit if absent.)

**Render every `display` field verbatim.** Any `ship_state`/`ship_prepare` response carrying a `display` or `resumeBriefing.display` field is pre-formatted markdown — print it as-is, never paraphrase or reformat it.

**Critical constraints** (full detail: [`reference.md`](reference.md)): no worktree isolation anywhere in this pipeline (`execute` uses a plain feature branch, set up before dispatch); call `ship_prepare` **at most once** per run — it initializes state as a side effect, and a second call re-seeds `steps[]` and destroys progress; this pipeline never creates or pushes a git tag itself — release intent (a bump level, optional pre-release label, and drafted notes) is resolved from `flags.bump` and forwarded to the pr step, which calls `pr_apply` to attach a `release:<level>` label and inject markers into the PR body, and the actual version bump/tag/CHANGELOG write happens post-merge via CI; no tool in this codebase pushes any branch either — that still requires a manual `AskUserQuestion` pause, in every automation mode, with no exception; `ship_state{action:"cleanup"/"cleanup-pipeline"}` **stamps** the state file terminal (`pipelineStatus:"completed"`) — it does not delete it.

Companion files, loaded on demand: [`config-format.md`](config-format.md) (`.sdlc-v2/local.json` fields), [`entry-modes.md`](entry-modes.md) (`--init-config`, `--gc`, `--dry-run`, `--resume` handlers), [`reference.md`](reference.md) (DO NOT, Error Recovery, Gotchas, Learning Capture), [`state-format.md`](state-format.md) (the `ship_state` on-disk schema).

---

## Step loop

**CLI evidence collection:** After every Bash tool call during this pipeline run,
call the state tool to log the execution:

```
ship_state({ action: "log-cli", detail: {
    step: "<current-step>",
    command: "<the-bash-command>",
    exitCode: <exit-code>,
    outputHead: "<first ~500 chars of output>"
}})
```

This data persists in `.sdlc-v2/evidence/cli-executions.jsonl` for MCP tool
coverage analysis. Best-effort — skip logging if the state call fails.

**0. Plan-mode check.** If the system context says plan mode is active: tell the user to exit plan mode and re-invoke `/ship`, then stop.

**1. Entry modes.** If `$ARGUMENTS` has `--init-config`, `--gc`, or `--resume`, read [`entry-modes.md`](entry-modes.md) and follow that handler, then stop (or continue from its resume point). Otherwise parse `--auto`, `--steps <csv>`, `--quick`, `--quality`, `--bump`, `--draft`, `--dry-run`, `--plan <path>`, `--openspec-change <name>`, `--ttl-days <N>`.

**2. Implicit resume check.** Call `ship_state({action:"read"})` even without `--resume` — a stale in-flight run can exist from a prior session. If the response carries `resumeBriefing` **and** `--resume` was explicitly passed (or `--auto` was not), follow [`entry-modes.md`](entry-modes.md)'s `--resume` handler now (render `resumeBriefing.display` verbatim, resume from `resumeBriefing.next`) — do not call `ship_prepare`. If `--auto` was passed **without** `--resume`, do not auto-follow `resumeBriefing` — proceed to a fresh run instead, but understand that `ship_prepare` (step 4) will **silently prune** that old resumable file as a side effect of writing new state (`state.Write`'s same-branch prune-on-write); it is not preserved. If no `resumeBriefing` at all (no file, a stamped-terminal file, or nothing ever started), continue below with no special handling.

**3. Pre-`ship_prepare` branch setup.** If on the default branch and `--plan <path>` was given:
  - Under `flags.auto`: `git checkout -b <name>` with a log line, no prompt.
  - Otherwise (interactive mode): AskUserQuestion before branch creation:
    > On the default branch ({branch}). A feature branch is needed.
    > Derived name: `{derivedName}`
    >
    > Options:
    > 1. **Create `{derivedName}`** — checkout and continue
    > 2. **Use a different name** — specify your own branch name
    
    On option 1: `git checkout -b {derivedName}`. On option 2: ask for the name, then `git checkout -b {userProvidedName}`. Either way, `ship_prepare` captures the branch synchronously.

**4. Call `ship_prepare`** with the parsed flags (`skipConfigCheck:false`, `hasPlan`, `auto`, `steps`, `quick`, `quality`, `bump`, `draft`, `dryRun`, `resume:false`, `rebase`, `openspecChange`, `planFile`, `gc:false`) → `{errors, warnings, flags, sources, branch, worktree, stateFile, prunedOrphans, report}`. Config auto-migrates internally (KD5 gate) — never hand-instruct a user to edit `schemaVersion`. `errors` non-empty: print each string verbatim, stop (no state was created). `warnings` non-empty: print each string verbatim, continue. `--dry-run`: render the pipeline table from `flags`/`sources` ([`entry-modes.md`](entry-modes.md)'s Dry-run section), then `ship_state({action:"skip", step, detail:{reason:"dry-run"}})` for every step in `flags.steps` and `ship_state({action:"cleanup-pipeline", detail:{force:false}})` to stamp the throwaway run terminal — `force:true` no longer deletes anything (it just preserves the file un-stamped), so it would leave a live-looking run behind for the next invocation to trip over. Then stop.

**5. `gh auth status`.** On failure, stop and tell the user to `gh auth login`.

**6. Confirmation.** If `flags.auto`, skip. Otherwise render a step-by-step summary from the Steps section below (each step's name plus its actual will-run/skip resolution from `flags.steps`) and confirm via `AskUserQuestion`. The confirmed list is binding — do not skip a "will run" step on your own judgment afterward.

**6b. Release-level confirmation.** Only when `pr` is present in `flags.steps` — a run with no `pr` step has no release intent to confirm, skip silently (`ship_prepare` itself rejects a CLI `--bump` given without `pr` configured — see reference.md's DO NOT list). Read `flags.bump` and `sources.bump` from `ship_prepare` (step 4) — the already-resolved bump level (cli > config > `version.preRelease` override > built-in default `"patch"`, then `version.preReleasePolicy: "always-rc"` enforces RC regardless of which tier won; `flags.bump` is never empty). Under `flags.auto`, skip the prompt entirely: no interactive confirmation happens in auto mode, so the level is always `releaseSource:"config"` (`pr_apply` rejects `releaseSource:"user"` under `autoMode` regardless of what's passed here). Otherwise, AskUserQuestion:

> Release level resolved to **{flags.bump}** (source: {sources.bump}).
>
> Options:
> 1. **Continue with {flags.bump}** — use the resolved level → `releaseSource:"config"`
> 2. **Change level** — pick a different level now → `releaseSource:"user"`

On option 2: ask which level (`major`/`minor`/`patch`, optionally with an RC pre-release label) and use it in place of `flags.bump` for the rest of this run. Hold the resolved value and its `releaseSource` in working context — see the `### pr` step below for how it's translated into `releaseLevel`/`releasePreRelease` and forwarded.

**7. Main loop.** For each of `execute, commit, review, verify-openspec, archive-openspec, pr, verify-pipeline, await-remote-review, learnings-commit` **present in `flags.steps`**, in that order — skip entirely (no `ship_state` call of any kind) for a name absent from `flags.steps`; there is no `steps[]` entry to skip against:

  a. Announce `→ <step>`.
  b. `ship_state({action:"begin-step", step}) → {todos, display, alreadyDone?, ...}`. `TodoWrite(todos)`; render `display` verbatim. If `alreadyDone` is true (this step's side effect is already verified in `sideEffects` from a resumed run), skip straight to (d) — do not re-dispatch.
  c. Do the work: Agent-dispatch (tracked steps) or inline work (inline steps) per the Steps section and Decisions & gates below.
  d. `ship_state({action:"complete-step", step, detail:{outcome:"success", result:"<summary>"}}) → {todos, display, issueCount, issueHighlights}`. `TodoWrite(todos)`; render `display` verbatim.
  e. On failure: `ship_state({action:"fail", step, detail:{reason, error, severity:"error", category:"ship-fail"}})`, then follow [`reference.md`](reference.md)'s Error Recovery table.
  f. Where a step can be legitimately bypassed at runtime despite being configured (no active OpenSpec change, review below threshold, etc.) call `ship_state({action:"skip", step, detail:{reason}})` instead of (c)/(d) — see Decisions & gates for exactly which steps.

**8. Between review's complete-step and the next configured main-loop step's begin-step** (`verify-openspec`, `archive-openspec`, or `pr` — whichever is first in `flags.steps`), sequence by hand — none of received-review, commit-fixes, or rebase belongs to the loop above, since none of the three has a `steps[]` entry: run the received-review/commit-fixes conditional (triggered by the review verdict), then rebase. See Decisions & gates for both.

**9. Terminal cleanup.** `ship_state({action:"cleanup-pipeline", detail:{force:false, ttlDays:<int, if --ttl-days given>}}) → {currentRun, gc, directories, force, ttlDays, issueSummary?}`. If `currentRun` reports a contract violation (`DataError`), the file was left un-stamped — show the violating steps and stop; do not pass `force:true` to paper over it (that skips the stamp entirely, it does not fix the violation).

**10. Summary.** Print step/status/result, the `decisions` log and `deferredFindings` (from the final `read`), and the cleanup outcome. `issueSummary` is present on Step 9's response only when `issues[]` is non-empty (`total`, `byCategory`, `items`, `display`, `hardenSuggestion`): render `issueSummary.display` verbatim, then append `issueSummary.hardenSuggestion` when present. When `issueSummary` is absent, render nothing — no "0 issues" line.

**10b. Deferred Follow-ups.** After rendering the summary, call `ship_state({action:"deferred_propose_followups"})`. If `openCount > 0`, render `display` verbatim, then:
- In `--auto` mode: skip entirely (no interactive prompts).
- Otherwise, AskUserQuestion:
  > {openCount} deferred issue(s) from previous runs still open.
  > Options:
  > 1. **Create GitHub issues** — open issues for unresolved items
  > 2. **Resolve selected** — mark items as resolved
  > 3. **Skip** — review later
- On option 1: for each selected deferred issue, `gh issue create` with label `deferred-followup`, body from the issue fields, then call `ship_state({action:"deferred_resolve", detail:{id:<id>}})`. Never append an AI-tool attribution line ("Generated with Claude Code" or similar) to the issue body.
- On option 2: AskUserQuestion for which to resolve, then call `ship_state({action:"deferred_resolve", detail:{id:<id>}})` for each selected item.
- On option 3: no action (items persist for next run).

When `openCount` is 0, render nothing.

**10c. Record run history.** `ship_state({action:"history_record", detail:{skill:"ship", branch:<branch>, outcome:<"success"|"failure"|"partial">, duration_ms:<elapsed>, steps:<step names>, version:<step 6b's held bump value, when the pr step ran with a resolved release level>}})`. There is no version-producing step anymore — `detail.version` is repurposed to hold the bump value forwarded to the pr step (see `### pr` below): `flags.bump` as resolved by `ship_prepare`, unless step 6b's interactive prompt let the user override it, in which case the override. Not a semver string a version step used to compute. Omit it when `pr` wasn't configured for this run. Non-fatal — if recording fails, log a warning and continue.

---

## Steps

Every Agent dispatch uses the fixed prompt shape in [`reference.md`](reference.md) ("Dispatch protocol") and never `isolation:"worktree"`. A dispatched skill is a black box — never call its own MCP tools directly from this skill (e.g. never call `pr_apply` here; only the pr Agent does). Tracking, dispatch, model, args, and pause behavior per step:

Four of the six `TrackedShipSteps` names — `execute`, `commit`, `review`, `pr` (the ones also present in `CanonicalSteps`/`ValidSteps`) — get a dedicated `steps[]` scaffold entry from `ship_prepare`; every other configured step name is `Kind:"inline"` in that same array. `received-review`/`commit-fixes` get **no** `steps[]` entry at all: `ship_prepare` seeds `steps[]` only from the configured step list, and `CanonicalSteps`/`ValidSteps` excludes both names, so they can never appear there even though `TrackedShipSteps` lists them — a known gap in `internal/shipmeta` (see their own entries below). All kinds still terminal-ize via `action:"begin-step"` → `action:"complete-step"` (or `action:"skip"`/`action:"fail"`) — an inline step that only ever calls `action:"decide"` sits `pending` forever and trips the cleanup contract check (see [`reference.md`](reference.md)'s DO NOT list). `action:"decide"` only appends to `decisions[]`; on an inline step it additionally records *why* (e.g. why a gate was bypassed) alongside the terminal call, it never substitutes for one.

### execute
Tracking: `action:"begin-step"` → `action:"complete-step"`. Dispatch: Agent → execute, model sonnet, args `--quality --rebase --wave-timeout --wave-interval <plan-path>` (never `--branch`, never `--auto`). Pauses only if plan-mode-blocked.

### commit
Tracking: `action:"begin-step"` → `action:"complete-step"`. Dispatch: Agent → commit, model haiku, args `--auto` (= `flags.auto`). No pause.

### review
Tracking: `action:"begin-step"` → `action:"complete-step"`. Dispatch: Agent → review, model sonnet, no args (or `--base <branch>`), never `--committed`. No pause.

### received-review (conditional)
Tracking: `action:"begin-step"` → `action:"complete-step"` per `TrackedShipSteps`, but currently **no `steps[]` entry exists to begin/complete against** — excluded from `CanonicalSteps`/`ValidSteps`, so `ship_prepare` never seeds one (see Steps section intro; a known gap in `internal/shipmeta`, not fixed here). It has no `flags.steps` entry either, so it's sequenced by hand — step 8 — not via the main loop; see Decisions & gates for exactly when it's triggered. Dispatch: Agent → received-review, model opus, args `[--pr <N>] [--auto]`. Pauses YES unless `flags.auto`.

### commit-fixes (conditional)
Tracking: `action:"begin-step"` → `action:"complete-step"` per `TrackedShipSteps`, same missing-`steps[]`-entry gap and hand-sequenced pattern as received-review (see its entry above). Dispatch: Agent → commit, model haiku, args `--auto`. No pause. Runs only if received-review made changes; produces a **separate** commit, never squashed with the feature commit. (Rebase happens next, between commit-fixes and the next configured main-loop step — see Decisions & gates. Rebase itself has no `steps[]` entry either; only `ship_state({action:"decide", step:"rebase", ...})` records its outcome.)

### verify-openspec (inline, opt-in)
Tracking: `action:"begin-step"` → `action:"complete-step"`, or `action:"skip"` when no active OpenSpec change. Inline `openspec validate "$CHANGE" --strict`, no Agent dispatch. Pauses YES on validation failure. An `action:"decide"` entry may record why it ran or was skipped.

### archive-openspec (inline)
Tracking: `action:"begin-step"` → `action:"complete-step"`, or `action:"skip"`. Inline Bash/grep/sed + `openspec archive "$CHANGE"`, no Agent dispatch. Pauses YES consent gate, unless `flags.auto`. An `action:"decide"` entry records the consent-gate outcome.

### pr
Tracking: `action:"begin-step"` → `action:"complete-step"`. Dispatch: Agent → pr, model sonnet, args `[--draft] [--base <branch>] [--auto]` (`--auto` = `flags.auto` — required so the pr skill's Step 1b release-intent gate and `pr_apply`'s `autoMode` enforcement both see it; omitting it would leave the pr skill unaware it's running inside an auto ship). `pr_prepare` now computes its own version diagnostics (`versionSource`, `bumpOptions`, `tags`, `conventionalSummary`, etc. — see `pr/SKILL.md`'s `PR_CONTEXT`); this skill no longer waits on a separate version step for any of it.

Translate step 6b's resolved bump value before dispatch: an exact `major`/`minor`/`patch` match forwards as `releaseLevel` with no `releasePreRelease`; any other bare label (matching `^[a-z][a-z0-9]*$`, e.g. `rc`, `beta`) forwards as `releaseLevel:"patch"` plus `releasePreRelease:<label>`. **Disclosed gap:** `pr_apply` currently accepts only `"rc"` for `releasePreRelease` (any other value is a `DomainError`), even though `flags.bump`/`ship.steps[].bump` accepts any `^[a-z][a-z0-9]*$` label and `version.preRelease` config advertises `beta`/`alpha` as examples — a non-`rc` label reaches `pr_apply` as a rejected call, which surfaces as a normal pr step failure here, not something to work around in this skill.

Instruct the Agent to include, in its own `pr_apply` call: `releaseLevel`, `releasePreRelease` (when set), `releaseSource` (held from step 6b — `"config"` or `"user"`), and a freshly drafted `releaseNotes` (a few bullet lines summarizing the change — the same drafting instructions as `pr/SKILL.md`'s Step 1b; the Agent still drafts these even though supplying `releaseLevel` at invocation skips that step's AskUserQuestion gate). Never forward `skipReleaseCheck` from this dispatch — `flags.bump` always resolves to a value (`ShipBuiltInDefaults.Bump` is `"patch"`), so a `pr` step configured in `flags.steps` always has a release level to forward; `skipReleaseCheck` exists only for a standalone `/pr` invocation with no ship dispatch. No pause.

### verify-pipeline (inline, opt-in)
Tracking: `action:"begin-step"` → `action:"complete-step"`. Inline `poll_await({target:"pipeline"})`, plus a conditional Agent → verify-pipeline dispatch (model sonnet, args `--pr <N> --auto`) when the poll resolves `failed`. Pauses YES manual-push after any auto-fix commit. An `action:"decide"` entry records the poll verdict.

### await-remote-review (inline, opt-in)
Tracking: `action:"begin-step"` → `action:"complete-step"`. Inline `poll_await({target:"remote_review"})`, plus a conditional Agent → received-review dispatch (model opus, args `--pr <N> [--auto]`) when the poll resolves `actionable`. Pauses YES unless `flags.auto`, on `actionable`. An `action:"decide"` entry records the poll verdict.

### learnings-commit (inline)
Tracking: `action:"begin-step"` → `action:"complete-step"`. Inline `learnings_log` call, no Agent dispatch. No pause — never touches git. An `action:"decide"` entry may record what was logged.

### Terminal cleanup
Not a `steps[]` entry — tracked directly via `action:"cleanup-pipeline"`, not a per-step action. Pauses YES on contract violation (`DataError`).

**After RC ships.** To promote an RC to a final release without creating another PR: go to
Actions → "Promote Release" → Run workflow, and enter the target version (e.g. `v1.3.0`).
The workflow finds the latest RC tag, aggregates release notes across all its RCs, bumps the
version file, writes the changelog, and creates the final tag + GitHub Release at the RC's
already-tested commit — no rebuild, no new PR. See [`docs/versioning.md`](../../../../docs/versioning.md#promoting-rc-to-final-release) for details.

---

## Decisions & gates

These are the judgment calls the tools cannot make for you — everything else in the loop above is mechanical.

- **Staging after execute.** `git add -A -- ':!.sdlc-v2/'` between `execute`'s complete-step and `commit`'s begin-step — execute doesn't stage. Skipping this produces an empty commit.
- **Review-verdict routing.** Parse `Verdict: <VERDICT>` from review's result text (missing verdict → treat as APPROVED WITH NOTES, warn). At or above `flags.reviewThreshold`: proceed to `received-review` (see below). Below threshold: `ship_state({action:"defer", step:"review", detail:{severity,file,title,line}})` per finding, then go straight to rebase — leave `received-review`/`commit-fixes` untouched. Neither has a `steps[]` entry (see Steps section) — there is nothing to be `pending`, so skip is silent and no `ship_state` call is needed to record the bypass.
- **received-review dispatch.** Both `received-review` and `commit-fixes` are in `shipmeta.InitialShipSteps()`'s 6-entry scaffold, so — when triggered — they use the same `begin-step`/`complete-step` pair as any main-loop step; they're just sequenced by hand (step 8) instead of via `flags.steps`. `received-review` runs only when the verdict is at/above threshold: `ship_state({action:"begin-step", step:"received-review"})` → Agent → received-review (opus, forwarding `--pr <N>` if a PR already exists and `--auto` under `flags.auto`) → `ship_state({action:"complete-step", step:"received-review", detail:{outcome:"success", result:"<summary>"}})`. `commit-fixes` follows the same pattern, running only if received-review made changes; it dispatches `commit` again — a **separate** commit from the feature commit, never squashed.
- **Rebase.** `git fetch origin <default>; git merge-base --is-ancestor origin/<default> HEAD` — already-ancestor skips rebase. Otherwise honor `flags.rebase` (`"auto"` rebases, `"skip"` never does; no `"prompt"` mode in this port — treat any other string as informational). Conflict: stop, tell the user to resolve and `--resume`. Always record: `ship_state({action:"decide", step:"rebase", detail:{text:"<outcome>"}})`.
- **Release-intent enforcement.** No manual pause, and no separate side-effect verification step — the old `"release-intent"` `sideEffects` kind is gone from `internal/tools/ship.go`'s `shipStepSideEffects` map along with the version step. `pr_apply` itself is the enforcement point: it rejects an invalid/missing `releaseLevel`, `releaseSource`, `releasePreRelease`, or an empty `releaseNotes` when `releaseLevel` is set, synchronously at call time (`DomainError`). A rejection surfaces through the pr step's own dispatch as a normal step failure (step loop item 7e, `ship_state({action:"fail", step:"pr", ...})`) — there is nothing extra for this skill to check.
- **verify-openspec / archive-openspec** run only when an active OpenSpec change exists (`flags.openspecChange` or auto-detected); if configured but no active change, `skip` with reason `"no active OpenSpec change"`. verify-openspec: `openspec validate "$CHANGE" --strict`; non-zero exit stops the pipeline. archive-openspec: check `openspec/changes/archive/$CHANGE` first (already archived → `skip` with reason, go to `pr`); else count `- [ ]`/`- [x]` in `tasks.md`, show a consent gate (`AskUserQuestion`, skipped under `flags.auto`), sync remaining boxes to `[x]` if archiving anyway, re-validate `--strict`, then `openspec archive "$CHANGE"`.
- **verify-pipeline poll loop.** `poll_await({target:"pipeline", pr})` loop: `pending` → sleep `interval_seconds`, resume with `state_file`; `error` → transient, re-probe; `done` → branch on `ext.verdict`: `skipped`/`timeout` → proceed; `green` → proceed; `failed` → dispatch verify-pipeline with `--logs "<ext.checks_raw>" --auto`, read its verdict line: `fix-applied` → commit `--auto`, **manual-push pause** (`AskUserQuestion`, then re-poll fresh, no `state_file`), repeat up to `flags.verifyPipelineMaxIterations`; `proposal` → show it, stop the auto-fix loop, treat as manual intervention; `abort` → record and proceed.
- **await-remote-review poll loop.** Same `pending`/`error`/`done` shape; on `done`: `skipped`/`timeout`/`approved-clean` → proceed; `actionable` → dispatch received-review (`opus`, `--pr`, `--auto`=`flags.auto`); a landed fix gets the same manual-push pause as above before proceeding.
- **learnings-commit.** Calls `learnings_log({action:"append", entry:"..."})` per [`reference.md`](reference.md)'s Learning Capture prompts — `.sdlc-v2/learnings/` is gitignored, this step never touches git or calls `commit_apply`.
- **Cleanup contract violation** (any `steps[]` entry left `pending` with no work done, or `in_progress`; `failed` never violates) means a step was left dangling — fix the actual step, don't force past it.

---

## Resume

`--resume`, or an implicit in-flight run detected at Step loop item 2, both defer entirely to [`entry-modes.md`](entry-modes.md)'s `--resume` handler: read state, check for `resumeBriefing`, render its `display` verbatim, continue from `resumeBriefing.next` using the state's existing `flags`/`steps`/`decisions`. Never call `ship_prepare` on a resume — it re-seeds `steps[]` from scratch. A step at `in_progress` when resumed is retried from the beginning, not assumed complete. Resume skips step 6b entirely (it defers straight to `resumeBriefing.next`, not back through steps 3-6b) — if the pr step is still ahead, its release level comes from the resumed state's persisted `flags.bump`/`sources.bump` (written verbatim by the original run's `ship_prepare` call) with `releaseSource:"config"`. **Disclosed gap:** unlike the old version-step journal, there is nothing in `sideEffects` that records a user's step-6b override (`releaseSource:"user"`) for resume to recover — if the original run's user picked a different level at 6b and the pipeline was interrupted before reaching the pr step, that override is lost on resume and the pr step re-derives the level from `flags.bump` instead. See [`state-format.md`](state-format.md)'s Resume section for the full `resumeBriefing` field shape, and `--gc`/`--dry-run`/`--init-config` handlers in [`entry-modes.md`](entry-modes.md) for the other short-circuit entry modes.

Full DO NOT list, Error Recovery matrix, Gotchas, and Learning Capture prompts: [`reference.md`](reference.md). Dispatched-skill docs: [`/execute`](../execute/SKILL.md), [`/commit`](../commit/SKILL.md), [`/review`](../review/SKILL.md), [`/received-review`](../received-review/SKILL.md), [`/pr`](../pr/SKILL.md), [`/verify-pipeline`](../verify-pipeline/SKILL.md).
