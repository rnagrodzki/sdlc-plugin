---
name: ship
description: "Use this skill to run the full ship pipeline: execute a plan, commit, review, version, open a PR, and optionally verify CI and await an automated reviewer. Orchestrates execute, commit, review, received-review, version, pr, and verify-pipeline as Agent-dispatched sub-skills, and runs a handful of steps (rebase, OpenSpec validate/archive, CI-poll, remote-review-poll, learnings commit) inline. Arguments: [--auto] [--steps <csv>] [--quick] [--quality full|balanced|minimal] [--bump patch|minor|major|<label>] [--draft] [--dry-run] [--resume] [--plan <path>] [--openspec-change <name>] [--gc] [--ttl-days <N>] [--init-config]. Triggers on: ship it, ship this, run the ship pipeline, full release pipeline, execute plan then release, ship."
user-invocable: true
argument-hint: "[--auto] [--steps <csv>] [--quick] [--quality full|balanced|minimal] [--bump patch|minor|major] [--draft] [--dry-run] [--resume] [--plan <path>] [--gc] [--init-config]"
model: sonnet
---

# Ship Pipeline (SDLC)

Orchestrates the full path from an approved plan to a merged-ready PR: `execute → commit → review → (received-review) → (commit-fixes) → version → (verify-openspec) → archive-openspec → pr → (verify-pipeline) → (await-remote-review) → learnings-commit → cleanup`. Every scaffolded pipeline step is dispatched as a separate Agent running its own ported sub-skill; a handful of steps have no sub-skill and run inline in this skill's own prose.

**Announce at start:** "I'm using ship (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

## Port Notes (read before using this skill)

This is a Go/MCP port. Read this section before running the pipeline — several behaviors that source automated are now manual, and one tool (`ship_prepare`) behaves differently than its name suggests.

- **No worktree/workspace isolation mode.** There is no `ship.workspace` config field and no Agent SDK worktree use anywhere in this pipeline. The `execute` step is isolated with a plain feature branch (`git checkout -b`) run by this skill itself before dispatch — see "Pre-execute workspace auto-detection" in Step 5. Never pass `isolation: "worktree"` to any Agent dispatch from this skill (R-agent-isolation-script-driven).
- **`ship_prepare` has no computed per-step dispatch plan.** Unlike source's `ship.js`, `ship_prepare`'s output carries no `dispatchMode`/`model`/`invocation` per step. Step 2's table below is the single source of truth for which steps are Agent-dispatched, at which model, and with which arguments.
- **`ship_prepare` already initializes pipeline state on success — never call it twice.** `shipPrepare`'s own doc comment states plainly that state is initialized internally whenever validation produces zero errors; it is not a pure validate-only call. A second call on an existing run silently re-seeds a fresh 7-step scaffold, discarding all prior progress. **Never call `ship_prepare` on `--resume`** — read existing state directly with `ship_state({action:"read"})` instead (see Step 1).
- **`--dry-run` is not side-effect-free in this port.** `ShipPrepareIn.DryRun` is recorded into the output's `Flags.dryRun` and nothing else — a valid (zero-error) dry-run call still creates a real state file exactly like a normal run. Step 1's dry-run handling below force-deletes that throwaway state immediately after rendering the table (`ship_state({action:"cleanup-pipeline", detail:{force:true}})`). This is a disclosed gap in the tool contract, not a design choice.
- **`ShipPrepareOut.Errors`/`.Warnings` are plain strings**, not `{id, message}` objects. Print each string verbatim; never invent an `id` field.
- **review has no `--committed`/`--staged`/`--working`/`--worktree` flags.** Dispatch it with no arguments (or `--base <branch>` when relevant) — scope comes from `.sdlc-v2/config.json`'s `review.scope` (default `all`), per `review/SKILL.md`'s own Port Notes. Do not pass `--committed`.
- **received-review is dispatched at `model: opus`**, not source's `sonnet` — matching that skill's own already-ported frontmatter.
- **Version and tag creation are split; tag creation and push are entirely manual.** `version_apply` bumps the version file and changelog only. No tool in this port creates or pushes a git tag (`internal/gitx` has no tag-mutation function). After the release commit lands, this pipeline must pause and ask the user to create and push the tag by hand before it can call `ship_verify_side_effect`. **This is a deliberate, documented exception to the general rule against `AskUserQuestion` in `--auto`/`automation.mode: unattended` mode** — no tool in this port can complete the version step's tag side effect unattended, in any automation mode. See "After version" in Step 5.
- **No tool pushes a branch to its remote, at any point, except implicitly.** There is no push-capable MCP tool anywhere in this codebase. The only push that happens automatically is `gh pr create`'s own implicit first push of the branch when `pr_apply` creates the PR. Any commit landing **after** the PR already exists (the verify-pipeline auto-fix commit, `learnings-commit`) is **not** pushed by any tool — `commit_apply` only runs `git add -A` + `git commit`, never `git push`. The pipeline must pause and ask the user to push before continuing to poll or before ending the run, for the same reason as the tag gap above (no executor tool exists to do it). See "verify-pipeline" and "learnings-commit" in Step 5.
- **`isArchived`, `getTaskCounts`, and `syncIncompleteTasks` (source's pure-JS OpenSpec helpers) have no MCP tool.** The archive-openspec section below hand-computes their equivalents with plain Bash/grep/sed. `validateChangeStrict` and `runArchive` remain literal Bash invocations of the external `openspec` CLI (there is no Go wrapper for either) — treat both as a disclosed, external-tool dependency, not a routing gap.
- **Bounded polling for `verify-pipeline` and `await-remote-review` lives in this skill**, not in `verify-pipeline` (which is a one-shot classify/fix skill with no loop of its own — see its own SKILL.md). Both polling tools return a `stepper.Envelope` with `status` one of `"pending"` / `"done"` / `"error"` in practice for these two tools (the type's doc comment also allows a `"step"` value, which neither tool ever emits — do not describe it as part of this pipeline's polling protocol).
- **Inline steps' automation mode has no per-step override in this port.** `ship_state({action:"next"})` resolves `automation.steps[...]` overrides only for the seven *scaffolded* steps (see Step 5's step list) — it cannot see the five inline step names at all. An `automation.steps` entry configured for an inline step name (e.g. `config-format.md`'s own example, `"verify-pipeline": "auto"`) has **no effect** in this port. Inline steps fall back to the pipeline-wide `flags.auto` boolean only.
- **`ship_prepare` has no `context`/`contextAdvisory` object.** All context this pipeline needs beyond `ship_prepare`'s own output (branch state, `gh` auth, PR existence) comes from direct `Bash`/`gh` calls in this skill's own prose, not from a precomputed advisory payload.

Companion files, loaded on demand (never preemptively): [`config-format.md`](config-format.md) (the `.sdlc-v2/local.json` `ship`/`automation` sections), [`entry-modes.md`](entry-modes.md) (`--init-config`, `--gc`, `--dry-run` handlers), [`reference.md`](reference.md) (error recovery, DO NOT, Gotchas, Learning Capture), [`state-format.md`](state-format.md) (the `ship_state` on-disk schema and its scaffolding gap).

---

## Step 0 — Plan Mode Check

If the system context contains "Plan mode is active":

1. Announce: "This skill requires write operations (executing tasks, committing, opening a PR). Exit plan mode first, then re-invoke `/ship`."
2. Stop. Do not proceed to subsequent steps.

---

## Step 1 (CONSUME): Entry modes, resume detection, `ship_prepare`

### 1a — Entry-mode short-circuits

Check `$ARGUMENTS` for `--init-config` or `--gc` **before** anything else. If present, read [`entry-modes.md`](entry-modes.md) and follow the matching handler exactly, then stop — neither entry mode proceeds to pipeline composition.

### 1b — Parse arguments

Parse from `$ARGUMENTS`: `--auto`, `--steps <csv>`, `--quick`, `--quality <full|balanced|minimal>`, `--bump <patch|minor|major|label>`, `--draft`, `--dry-run`, `--resume`, `--plan <path>`, `--openspec-change <name>`, `--ttl-days <N>`.

### 1c — Resume detection

Call `ship_state({action:"read"})`.

- **Result is the `DataError` string `no ship state found for branch "<branch>"`** — no prior run on this branch. Proceed to 1d (fresh path).
- **Result is a successful state object** — a prior run exists. Skip `ship_prepare` entirely for this invocation (see Port Notes). Treat the returned `flags`/`steps`/`decisions`/`deferredFindings` as the effective prepare output and proceed directly to Step 2 using this data. If `--resume` was not passed and `--auto` was, source's rule still applies: auto mode does **not** auto-resume without an explicit `--resume` — start a fresh run instead (the stale state file is left in place, not deleted).
- **Any other error** — treat as an unrecoverable state-read failure. Show the error and stop.

### 1d — Fresh path: pre-execute branch auto-detection

`ship_prepare` captures the current branch synchronously and files state against it — a branch created *after* calling it is too late. Before calling `ship_prepare`, check the current branch:

- If `CURRENT_BRANCH` equals the repo's default branch **and** `--plan <path>` was given (`HasPlan` will be true), create and check out a new feature branch now (`git checkout -b <name>`), then proceed with `ship_prepare` on that branch.
- Otherwise leave the branch as-is. If the run ends up on the default branch anyway, `ship_prepare`'s own not-on-default-branch warning (see below) surfaces it — do not pre-empt that warning by branching speculatively.

### 1e — Call `ship_prepare`

```
ship_prepare({
  skipConfigCheck: false,
  hasPlan: <bool>,
  auto: <bool from --auto>,
  steps: <string[] from --steps, or omit>,
  quick: <bool from --quick>,
  quality: <string from --quality, or omit>,
  bump: <string from --bump, or omit>,
  draft: <bool from --draft>,
  dryRun: <bool from --dry-run>,
  resume: <bool from --resume>,
  rebase: <string, or omit>,
  openspecChange: <string from --openspec-change, or omit>,
  hookActivePipeline: false,
  planModeBlocked: false,
  planFile: <string from --plan, or omit>,
  gc: false,
  sessionID: ""
}) → { errors, warnings, action, report, flags, sources, branch, worktree, stateFile, prunedOrphans }
```

**On tool error:** show the error and stop.

**If `errors` is non-empty:** print each string verbatim, then stop. No state was created (the early-return happens before state initialization).

**If `warnings` is non-empty:** print each string verbatim, then continue. Recognize (do not paraphrase) at least: the reserved-step message, the unrecognized-step message (`Unrecognized step %q in %s. Valid values: %s`), the `--bump` without version-step message, the `execute.commitWaves` non-boolean message, the unconditional review-pause reminder ("If review finds critical/high issues, pipeline will pause for fix approval" — always present, not a real warning about *this* run), and the not-on-default-branch message.

**If `--dry-run` was passed:** `errors` was empty (otherwise the block above already stopped), so a throwaway state file now exists. Render the dry-run table from `flags` (see [`entry-modes.md`](entry-modes.md)'s Dry-run mode section for the exact table format, substituting the real resolved `flags`/`sources`), then immediately clean it up:

```
ship_state({action:"cleanup-pipeline", detail:{force:true}}) 
```

`force:true` is required — a freshly-scaffolded `execute` step sitting at `pending` with no `condition` key would otherwise fail the cleanup contract check. Stop after this; do not proceed to Step 2.

Otherwise, treat `flags`/`sources`/`stateFile` as the effective prepare output and proceed to Step 2.

---

## Step 2 (PLAN): Pipeline composition

Compose the pipeline table from `flags.steps` (resolved by `ship_prepare` per `config-format.md`'s Merge Precedence: explicit `steps` > `quick` > `.sdlc-v2/local.json ship.steps[]` > built-in defaults) plus the two conditional sub-steps (`received-review`, `commit-fixes`) and the terminal `cleanup` action, none of which are members of `flags.steps` themselves.

Fixed execution order (matches `config-format.md`'s own canonical ordering):

`execute → commit → review → (received-review) → (commit-fixes) → [rebase] → version → (verify-openspec) → (archive-openspec) → pr → (verify-pipeline) → (await-remote-review) → learnings-commit → cleanup`

For every name present in `flags.steps`, mark it "will run"; for every canonical name absent from `flags.steps`, mark it "skipped." `received-review` and `commit-fixes` are always "conditional" (they trigger on the review verdict, not on `flags.steps` membership). `rebase` and `cleanup` are not `flags.steps` members and always run (rebase auto-skips if the default branch is already an ancestor; cleanup always runs at the end).

| Step | Dispatch | Model | Args (forwarded) | Pauses? |
|---|---|---|---|---|
| execute | Agent → execute | sonnet | `--quality`, `--rebase`, `--wave-timeout`, `--wave-interval`, `<plan-file-path>` — never `--branch`, never ship's own `--auto` (different meaning there) | Only if plan-mode-blocked |
| commit | Agent → commit | haiku | `--auto` (ship's `flags.auto`) | no |
| review | Agent → review | sonnet | none (or `--base <branch>`) — never `--committed` | no |
| received-review | Agent → received-review | **opus** | `[--pr <N>] [--auto]` | YES, unless `flags.auto` |
| commit-fixes | Agent → commit | haiku | `--auto` | no |
| rebase | inline Bash | — | — | YES on conflict |
| version | Agent → version | haiku | `[<bump>] [--pre <label>] [--auto]` | YES — manual tag-and-push pause (Port Notes) |
| verify-openspec | inline `openspec` CLI (Bash) | — | `--strict` | YES on validation failure |
| archive-openspec | inline Bash/grep/sed + `openspec` CLI | — | — | YES consent gate, unless `flags.auto` |
| pr | Agent → pr | sonnet | `[--draft] [--base <branch>]` | no |
| verify-pipeline | inline poll (`poll_await({target: "pipeline"})`) + conditional Agent → verify-pipeline | sonnet | `--pr <N> --auto` (dispatch only) | YES manual-push pause after any auto-fix commit |
| await-remote-review | inline poll (`poll_await({target: "remote_review"})`) + conditional Agent → received-review | opus | `--pr <N> [--auto]` (dispatch only) | YES, unless `flags.auto`, on `actionable` verdict |
| learnings-commit | inline `commit_apply` call | — | — | YES manual-push pause if a commit landed |
| cleanup | `ship_state({action:"cleanup-pipeline"})` | — | — | YES on contract violation |

**Review-verdict conditional dispatch:** `received-review` triggers when the review verdict's highest severity meets or exceeds `flags.reviewThreshold` (`critical` / `high` / `medium`, see `config-format.md`'s reviewThreshold Levels table). Below threshold, findings are reported and deferred (`ship_state({action:"defer", ...})`) instead of triggering the fix loop.

---

## Step 3 — Additional validation

`ship_prepare` already validated step composition, quality, bump, and quick-profile consistency. The one live check it cannot perform is GitHub auth, since it never shells out to `gh`:

```bash
gh auth status
```

On failure, stop and tell the user to run `gh auth login` — do not proceed to confirmation.

---

## Step 4 — Confirmation

If `flags.auto` is `true`, skip confirmation and proceed directly to Step 5.

Otherwise, display the Step 2 pipeline table and ask for confirmation via `AskUserQuestion` before dispatching anything. Treat the confirmed table as binding for the rest of the run (see `reference.md`'s "Pipeline plan is binding" Gotcha) — do not skip a step marked "will run" based on your own judgment of the change's risk or size.

---

## Step 5 (DO): Execution

### Dispatch protocol

Every Agent-dispatched sub-skill uses this exact prompt shape, and never `isolation: "worktree"` (or any `isolation` value):

```
You are executing the <skill-name> skill. Invoke `/<skill-name> <args>` using the Skill tool — this loads the SKILL.md automatically. Return a structured result:
(1) status — success or failure
(2) result summary — 2-3 lines
(3) artifacts — commit hash, tag, PR URL, verdict, etc.
(4) any warnings or issues encountered
```

Each dispatched skill is a black box from this pipeline's point of view — never call a dispatched skill's own MCP tools directly (e.g. never call `version_prepare`/`version_apply` from ship itself; only the version Agent calls those). The version dispatch specifically must be instructed to report its computed `NEW_TAG` (`tags.tagPrefix + newVersion`, per `version/SKILL.md`'s own Step 8) in its artifacts section — this is the only way this pipeline learns the tag name for the manual tag-and-push pause and the later ancestry gate.

### TodoWrite orchestration

`ship_state({action:"begin-step", step:<name>})` and `ship_state({action:"complete-step", step:<name>, detail:{...}})` each already return a bundled `{todos:[...]}` render inline — do not make a separate follow-up `ship_state({action:"todos"})` call after either. Feed that bundled array straight into `TodoWrite`. Use the standalone `ship_state({action:"todos"})` action only to re-render the checklist after something else changed it out of band (a `decide` call on an inline step, or right after resuming).

### Pre-execute workspace auto-detection

Already performed in Step 1d, before `ship_prepare` was called. Do not repeat it here.

---

### execute

```
ship_state({action:"begin-step", step:"execute"}) → { todos }
```

`TodoWrite(todos)`. Dispatch execute per the Step 2 table, forwarding only `--quality`, `--rebase`, `--wave-timeout`, `--wave-interval`, and the plan file path — never `--branch` (the branch was already set up in Step 1d) and never ship's own `--auto` (execute's `--auto` flag has a different meaning and must not be conflated with the pipeline-wide flag).

After execute's structured result comes back, run `execute_state({action:"verify-completeness"})` (the Go-native replacement for source's `execute.js verify-completeness`, exit 65). On `{ok:true, ...}`, continue. On a `DataError` reporting missing IDs, stop and surface it — the invariant that every planned task landed somewhere in a wave was violated.

```
ship_state({action:"complete-step", step:"execute", detail:{outcome:"success", result:"<2-3 line summary>"}}) → { todos }
```

`TodoWrite(todos)`.

### Between execute and commit — staging

execute creates and modifies files but does not stage them:

```bash
git add -A -- ':!.sdlc-v2/'
```

Missing this step produces an empty commit.

### commit

```
ship_state({action:"begin-step", step:"commit"}) → { todos }
```

`TodoWrite(todos)`. Dispatch commit with `--auto` set to `flags.auto`.

```
ship_state({action:"complete-step", step:"commit", detail:{outcome:"success", result:"<commit sha/summary>"}}) → { todos }
```

### review

```
ship_state({action:"begin-step", step:"review"}) → { todos }
```

`TodoWrite(todos)`. Dispatch review with no arguments (or `--base <branch>`) — never `--committed` (Port Notes). Parse the verdict from the sub-skill's result text (`Verdict: <VERDICT>`, per `reference.md`'s "Verdict detection is text-based" Gotcha — treat a missing verdict as APPROVED WITH NOTES and warn).

```
ship_state({action:"complete-step", step:"review", detail:{outcome:"success", result:"<verdict>"}}) → { todos }
```

### Between review and received-review — routing

If the verdict's highest severity is below `flags.reviewThreshold`: defer every finding (`ship_state({action:"defer", step:"review", detail:{severity, file, title, line}})` per finding) and skip `received-review` (`ship_state({action:"skip", step:"received-review", detail:{reason:"below threshold"}})`), then skip `commit-fixes` the same way, and go straight to rebase.

If at or above threshold, proceed to received-review.

### received-review (conditional)

```
ship_state({action:"begin-step", step:"received-review"}) → { todos }
```

`TodoWrite(todos)`. Dispatch received-review **at `model: opus`** (Port Notes), forwarding `--pr <N>` if a PR already exists from a prior partial run, and `--auto` when `flags.auto`. Under `--auto`, both the consent prompt and the reply/resolve prompt are skipped, "will fix" items are auto-implemented and their threads auto-resolved, and "disagree" items are replied to but left open. Without `--auto`, the pipeline pauses here for human approval (`reference.md`'s "received-review supports `--auto`" Gotcha).

```
ship_state({action:"complete-step", step:"received-review", detail:{outcome:"success", result:"<n items fixed>"}}) → { todos }
```

### commit-fixes (conditional)

Runs only if received-review made changes.

```
ship_state({action:"begin-step", step:"commit-fixes"}) → { todos }
```

`TodoWrite(todos)`. Dispatch commit again with `--auto` set to `flags.auto`. This is a **separate** commit from the feature commit — do not squash them (`reference.md`'s "Double commit is intentional" Gotcha).

```
ship_state({action:"complete-step", step:"commit-fixes", detail:{outcome:"success", result:"<fix commit sha>"}}) → { todos }
```

If `received-review` was skipped, skip `commit-fixes` too (`ship_state({action:"skip", step:"commit-fixes", detail:{reason:"received-review skipped"}})`).

### Rebase (before version, no scaffolded step)

```bash
git fetch origin <default-branch>
git merge-base --is-ancestor origin/<default-branch> HEAD
```

If the default branch is already an ancestor (exit 0), skip the rebase — no fetch/rebase overhead needed. Otherwise, honor `flags.rebase` (`"auto"` rebases onto the default branch; `"skip"` never rebases — see `config-format.md`'s rebase field; there is no `"prompt"` mode in this port, treat any other string as informational only):

```bash
git rebase origin/<default-branch>
```

On conflict, stop, save state, and tell the user to resolve in place and `--resume`. Record the outcome for audit purposes: `ship_state({action:"decide", step:"rebase", detail:{text:"<rebased onto <default-branch>|skipped: ancestor|skipped: flags.rebase=skip>"}})`.

### version

```
ship_state({action:"begin-step", step:"version"}) → { todos }
```

`TodoWrite(todos)`. Dispatch version per the Step 2 table, instructing it (per the Dispatch protocol above) to report `NEW_TAG` in its artifacts. version's own pipeline lands the release commit via `commit_apply` internally — its result is the only signal this pipeline needs.

**Manual tag-and-push pause (intentional exception to "never `AskUserQuestion` in `--auto`").** After version's dispatch returns, use `AskUserQuestion` (even under `flags.auto`/`automation.mode: unattended`) to tell the user:

```
Release commit landed. This port cannot create or push a git tag — no tool in this
codebase mutates git tags. Please run:

  git tag <NEW_TAG>
  git push origin <NEW_TAG>

then confirm to continue.
```

Only after the user confirms, call:

```
ship_verify_side_effect({step:"version", expected: NEW_TAG}) → { landed, sideEffect, expected }
```

If `landed` is `false`, show `sideEffect`/`expected` and re-prompt — do not fabricate a passing check, and do not silently skip it and proceed to `pr`.

Once `landed` is `true`, run the ancestry hard gate:

```
verify_tag_ancestry({tag: NEW_TAG}) → { ok, details }
```

If `ok` is `false`, stop and show `details` verbatim — do not proceed to `pr` with a tag that landed on the wrong branch (this gate's non-ancestor message tells the user exactly how to delete the tag and retry).

```
ship_state({action:"complete-step", step:"version", detail:{outcome:"success", result: NEW_TAG}}) → { todos }
```

If `version` is not in `flags.steps`: `ship_state({action:"skip", step:"version", detail:{reason:"not in configured steps"}})` and skip both the tag pause and the ancestry gate — there is no `NEW_TAG` to check.

### verify-openspec (inline, opt-in)

Only runs if `verify-openspec` is in `flags.steps` and an active OpenSpec change exists (`flags.openspecChange`, or auto-detected).

```bash
openspec validate "$CHANGE_NAME" --strict
```

On non-zero exit, stop and show the CLI's output — do not archive an unvalidated change. On success:

```
ship_state({action:"decide", step:"verify-openspec", detail:{text:"openspec validate --strict: passed"}})
```

(No `begin-step`/`complete-step` — `verify-openspec` has no `steps[]` entry; see `state-format.md`'s scaffolding gap.)

### archive-openspec (inline)

Only runs if an active OpenSpec change exists.

**`isArchived` equivalent** — check before doing anything else:

```bash
[ -d "openspec/changes/archive/$CHANGE_NAME" ] && echo archived || echo not-archived
```

If already archived, record `ship_state({action:"decide", step:"archive-openspec", detail:{text:"already archived, no-op"}})` and move on to `pr`.

**`getTaskCounts` equivalent:**

```bash
total=$(grep -c '^- \[[ xX]\]' "openspec/changes/$CHANGE_NAME/tasks.md")
done=$(grep -c '^- \[[xX]\]' "openspec/changes/$CHANGE_NAME/tasks.md")
```

**Consent gate** (skipped when `flags.auto`): show `$done/$total` tasks complete and the change name, ask for confirmation via `AskUserQuestion` before archiving.

**`syncIncompleteTasks` equivalent** — if `$done` < `$total` and the user (or `flags.auto`) confirms archiving anyway, mark the remaining boxes complete before archiving (macOS/BSD sed shown; use `sed -i` without the empty string argument on GNU/Linux):

```bash
sed -i '' 's/^- \[ \]/- [x]/' "openspec/changes/$CHANGE_NAME/tasks.md"
git add "openspec/changes/$CHANGE_NAME/tasks.md"
```

**`validateChangeStrict`** (re-run after any sync):

```bash
openspec validate "$CHANGE_NAME" --strict
```

**`runArchive`:**

```bash
openspec archive "$CHANGE_NAME"
```

Check your `openspec` CLI's actual flags for a non-interactive/yes mode before scripting this in an `--auto` run — this pipeline has no visibility into whether the installed `openspec` binary prompts.

```
ship_state({action:"decide", step:"archive-openspec", detail:{text:"archived: <$done>/<$total> tasks"}})
```

### pr

```
ship_state({action:"begin-step", step:"pr"}) → { todos }
```

`TodoWrite(todos)`. Dispatch pr per the Step 2 table (`--draft` from `flags.draft`).

```
ship_state({action:"complete-step", step:"pr", detail:{outcome:"success", result:"<PR URL>"}}) → { todos }
```

Extract and remember the PR number from the returned URL — every inline step from here on needs it.

### verify-pipeline (inline, opt-in)

Only runs if `verify-pipeline` is in `flags.steps`.

```
poll_await({target: "pipeline", pr: PR_NUMBER}) → Envelope
```

Loop:

- `status:"pending"` — sleep `interval_seconds` (from `progress`, default 60s), then re-call with `state_file: <the returned state_file>` to resume the same poll window.
- `status:"error"` — surface `error` and treat as a transient `gh`/network failure; re-probe on the next turn rather than aborting immediately.
- `status:"done"` — branch on `ext.verdict`:
  - `"skipped"` (`reason:"exhausted"`) or `"timeout"` — record `ship_state({action:"decide", step:"verify-pipeline", detail:{text:"<verdict>: <detail>"}})` and proceed to `await-remote-review`.
  - `"green"` — record the decision and proceed.
  - `"failed"` — dispatch verify-pipeline (model sonnet) with `--pr PR_NUMBER --logs "<ext.checks_raw>" --auto` (forwarding the raw check-name/state text; there is no full CI log fetch in this port — see `verify-pipeline/SKILL.md`'s own Gotchas on `checks_raw` coarseness). Read its single JSON verdict line:
    - `fix-applied` — dispatch commit `--auto` to commit the fix. **Manual-push pause**: since no tool in this port pushes, use `AskUserQuestion` to tell the user to `git push` the fix commit now, then re-poll with `poll_await({target: "pipeline", pr: PR_NUMBER})` (fresh call, no `state_file` — a new CI run needs a new poll window). Repeat up to `flags.verifyPipelineMaxIterations` times; on exhaustion, stop and report.
    - `proposal` — show the proposal, stop the auto-fix loop, and treat this like a manual-intervention pause.
    - `abort` — record the reason as a skip-with-warning and proceed to `await-remote-review`.

If `verify-pipeline` is not in `flags.steps`, skip this section entirely (record no decision — there is nothing to decide).

### await-remote-review (inline, opt-in)

Only runs if `await-remote-review` is in `flags.steps`.

```
poll_await({target: "remote_review", pr: PR_NUMBER}) → Envelope
```

Same `pending`/`error`/`done` loop shape as above (defaults: 600s timeout, 60s interval, reviewers `["copilot"]`). On `status:"done"`, branch on `ext.verdict`:

- `"skipped"` (exhausted) or `"timeout"` — `ship_state({action:"decide", step:"await-remote-review", detail:{text:"<verdict>: <detail>"}})`, then proceed to `learnings-commit`.
- `"approved-clean"` — `ship_state({action:"decide", step:"await-remote-review", detail:{text:"approved-clean"}})`, then proceed.
- `"actionable"` — dispatch received-review **at `model: opus`**, `--pr PR_NUMBER`, `--auto` set to `flags.auto`. When the fix lands via commit, the same manual-push pause applies as in verify-pipeline above (`AskUserQuestion`, then push, before re-polling or proceeding). Record `ship_state({action:"decide", step:"await-remote-review", detail:{text:"actionable: fixed via received-review"}})` once the fix is committed and pushed.

If `await-remote-review` is not in `flags.steps`, skip this section entirely.

### learnings-commit (inline)

Runs the ship-level Learning Capture (see [`reference.md`](reference.md)'s Learning Capture section for the exact prompts/format), appending to `.sdlc-v2/learnings/log.md`, then commits it directly — deliberately not via a commit Agent dispatch, since this is a single-file, non-narrative append with a fixed message:

```
commit_apply({message: "chore(ship): capture pipeline learnings", skipConfigCheck: false, sessionID: ""}) → { sha }
```

- **On a `DataError` reading "nothing to commit after staging"** (i.e. the log file had no new content, or the working tree was already clean): report "learnings-commit: no-op (no new learnings)" and continue — this is the expected no-op path, not a failure. Record it: `ship_state({action:"decide", step:"learnings-commit", detail:{text:"no-op: nothing to commit"}})`. Do not treat it as an error.
- **On success:** a real commit landed after the PR was created. Record it: `ship_state({action:"decide", step:"learnings-commit", detail:{text:"committed <sha>"}})`. No tool pushes it — use `AskUserQuestion` to tell the user to run `git push` before ending the session, mirroring the same manual-push gap disclosed above.

If `learnings-commit` is not in `flags.steps`: `ship_state({action:"decide", step:"learnings-commit", detail:{text:"skipped: not in configured steps"}})` and do nothing (`execute`'s own Learning Capture, recorded in the feature commit, still applies regardless of this step's status).

### Terminal cleanup

Before cleaning up, read state once more to decide whether to force the cleanup:

```
ship_state({action:"read"}) → data
```

If any scaffolded step's status is `"failed"`, the contract check would block a normal cleanup — pass `force:true` and note why. Otherwise call without `force`:

```
ship_state({action:"cleanup-pipeline", detail:{force: <bool>, ttlDays: <int, if --ttl-days was given>}}) → { currentRun, gc, force, ttlDays }
```

Surface the JSON report. If `currentRun.valid` is `false` (a pipeline contract violation reported without `force`), the state file was preserved — show the violation list and stop; do not force-delete it yourself without telling the user why the contract check failed.

---

## Step 6 — Pipeline summary

Print a summary table (step / status / one-line result), the `decisions` log (from the final `ship_state({action:"read"})`, or from tracking each `decide` call inline), the `deferredFindings` list (if any), and the terminal cleanup outcome. If OpenSpec steps ran, add a line noting validate/archive outcome. If the state file was preserved due to a contract violation, say so and give the `--resume` instruction.

---

## Quality Gates

- Every scaffolded step transitions through `begin-step` → (dispatch/inline work) → `complete-step`/`skip`/`fail` — never left `in_progress` at the end of a turn.
- The version step's tag-and-push pause and ancestry gate both ran before `pr` was dispatched, whenever `version` was in `flags.steps`.
- Every post-PR commit (verify-pipeline auto-fix, learnings-commit) was followed by an explicit manual-push confirmation from the user.
- `gh auth status` passed before Step 4's confirmation.

## Best Practices

1. Treat the confirmed Step 2 table as a contract — do not skip a "will run" step based on your own risk assessment.
2. Read `flags`/`sources` to explain *why* a step is running or skipped, instead of re-deriving the reason (`config-format.md`'s "`sources` tracks provenance" Gotcha).
3. Keep the feature commit and the review-fix commit separate — never squash them.
4. Resume by reading state, never by re-calling `ship_prepare`.

## DO NOT

See [`reference.md`](reference.md)'s DO NOT list for the full 18-item catalogue (dispatch-table deviations, parallel step execution, deleting state on failure, skipping the ancestry gate, treating `--auto` as license to skip the manual tag pause, calling inline-step progress actions with an inline step name, etc.) — not reproduced here to avoid drift between two copies of the same list. Two items specific to this port, not in that file:

- Do not call `ship_prepare` more than once per run (see Port Notes) — a second call re-seeds state and destroys progress.
- Do not invoke `git push` yourself via Bash after a post-PR commit. No push-capable tool exists in this port; the manual-push pause (`AskUserQuestion`) is the only correct path, matching the same reasoning as the tag-creation gap.

## Error Recovery

See [`reference.md`](reference.md)'s Error Recovery table for the full failure/recovery matrix (sub-skill dispatch failure, `gh auth` failure, staging failure, transient `gh` errors during polling, `ship_state` write failure, corrupt/missing resume state, unparseable review verdict, sub-skill timeout) and the exact resume-instruction format to print on any unrecoverable step failure.

## Gotchas

See [`reference.md`](reference.md)'s Gotchas section for the full list (staging gap after execute, text-based verdict detection, double-commit intentionality, config-optionality, step-set validation, `.sdlc-v2/` gitignore requirement, binding pipeline plan, tool-managed state files, no-worktree isolation, rebase-before-version ordering, auto-mode-does-not-auto-resume, context-isolated sub-skill dispatch, `sources` provenance, no-workspace-config). Two additions specific to this file, not in that document:

- **`ship_prepare`'s `DryRun` has no real effect on its own** — a valid dry-run call creates real state; this skill's own dry-run handling (Step 1e) force-cleans it up immediately after rendering the table.
- **Inline steps ignore per-step `automation.steps[...]` overrides** — only the pipeline-wide `flags.auto` boolean reaches `verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, and `learnings-commit`.

## Learning Capture

See [`reference.md`](reference.md)'s Learning Capture section for the exact prompts and `.sdlc-v2/learnings/log.md` format used by the `learnings-commit` step above.

## What's Next

After a clean run: the PR is open, CI is green (if `verify-pipeline` ran), and an automated reviewer's verdict has been handled (if `await-remote-review` ran). Suggest the user request a human review, or merge if their process allows it.

## See Also

- [`/execute`](../execute/SKILL.md) — dispatched by the `execute` step
- [`/commit`](../commit/SKILL.md) — dispatched by `commit`, `commit-fixes`, and the verify-pipeline auto-fix path
- [`/review`](../review/SKILL.md) — dispatched by the `review` step
- [`/received-review`](../received-review/SKILL.md) — dispatched by `received-review` and `await-remote-review`'s actionable path
- [`/version`](../version/SKILL.md) — dispatched by the `version` step
- [`/pr`](../pr/SKILL.md) — dispatched by the `pr` step
- [`/verify-pipeline`](../verify-pipeline/SKILL.md) — dispatched by the `verify-pipeline` step's failed-CI path
