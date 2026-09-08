---
name: plan
description: "Use when writing an implementation plan from requirements, a spec, a design doc, or a user description. ALWAYS use when plan mode is active — this is the designated plan-mode skill. Analyzes scope, maps file structure, decomposes into classified tasks with dependencies, and produces a plan ready for execute. Triggers on: write plan, create plan, plan this, break this into tasks, implementation plan, plan mode."
user-invocable: true
argument-hint: "[--auto] [--spec] [--from-openspec <change-name>] [spec-file-path]"
model: opus
---

# Plan (SDLC)

Write an implementation plan from requirements, a spec, or a user description. Produces a plan in the format consumed by execute — with per-task complexity/risk/dependency metadata embedded.

**Announce at start:** "I'm using plan (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

<!-- Decision-framing rule (applies to every AskUserQuestion in this skill) -->
**Decision-framing rule:** Before calling AskUserQuestion, frame the decision so the user never needs the full project context in memory to answer:
1. **What:** one sentence on what is being decided
2. **Why it matters:** one sentence on what this choice affects downstream
3. **Options:** each option carries a concrete consequence, not just a label

Scale framing to `style.audience` (extracted from the `plan_prepare` output — see Step 0's "Template resolution" call; one of `"technical"`, `"executive"`, `"mixed"`):
- `"technical"` — reference files, functions, config fields
- `"executive"` — reference features, user impact, timeline
- `"mixed"` — lead with impact, follow with implementation detail

`style.audience` is not resolved yet during Step 0's OpenSpec-integration questions (they run before the `plan_prepare` call) — use plain, audience-neutral language there instead.

Example — OpenSpec gate check:
> This project uses OpenSpec for tracking requirements as structured specs.
> Your change looks like a new feature. How should we handle requirements?
> 1. **Start OpenSpec flow** — creates a formal spec document first, then plans from it. Best for features that need stakeholder review.
> 2. **Generate specs inline** — plan generates requirement artifacts as part of the plan itself. Faster, good for well-understood changes.
> 3. **Use existing spec** — a spec already exists for this work.

## Step 0: Mode Detection, Routing, and Setup

**Mode detection:** Check whether a system-reminder contains "Plan mode is active". If yes, extract the designated plan file path from "You should create your plan at `<path>`". That path is the only writable file.

**Gather requirements:** If no spec or requirements document is in context, use AskUserQuestion. Context: this description is the plan's only input — task scope, decomposition, and verification all derive from it, so a vague answer here produces a vague plan.
> What do you want to implement? (describe in free form, bullet points, or provide a file path)

**OpenSpec integration (opt-in — requires `--spec` flag or explicit spec path):**

**Hook context fast-path:** If the session-start system-reminder contains an `OpenSpec active:` line, use its data (change name, branch match status, delta spec count) to skip the initial `Glob for openspec/config.yaml` and change directory scanning. If the line is absent or the user switched branches since session start, fall back to the existing Glob-based detection. The hook context is a session-start snapshot — treat it as a hint, not as authoritative.

1. Glob for `openspec/config.yaml`. If absent, skip this entire block — no OpenSpec in this project.
2. **Gate check:** If `openspec/config.yaml` exists but neither `--spec` flag was passed NOR the user provided a path into `openspec/changes/`:
   a. **Classify the request:** Determine whether the user's task involves functional changes (new features, behavior modifications, API changes, new integrations, capability additions) vs non-functional changes (refactoring, config, docs, CI/CD, dependency updates, formatting, infrastructure).
   b. **Non-functional changes:** Print:
      > OpenSpec detected — pass `--spec` to include spec context in planning.
      Then skip the rest of this block. `openspecContext` remains empty.
   c. **Functional changes:** Check whether an active OpenSpec change already covers this work — Glob `openspec/changes/*/proposal.md` (exclude `archive/`), and if any exist, try matching against the current git branch name. If a match is found, treat it as if the user passed `--spec` and continue to step 3. If no match, use AskUserQuestion. Frame per the decision-framing rule in the preamble — `style.audience` is not resolved yet this early in Step 0, so keep the language plain and audience-neutral:
      > This project uses OpenSpec — spec-driven development where a change gets a written proposal, delta specs (the requirements), and a task list before implementation starts. Your request looks like a new feature, and no `--spec` flag or existing change path was given.
      >
      > Options:
      > 1. **Start OpenSpec flow** — use the openspec CLI to author a change first (proposal + delta specs written before this plan proceeds). Best for non-trivial features that need stakeholder review before implementation starts.
      > 2. **Generate OpenSpec artifacts as plan appendix** — plan generates proposal, spec deltas, and tasks as inline appendix content (recommended default). Faster — no separate CLI round trip; the artifacts ship inside this plan for a later `openspec create`/`openspec validate`.
      > 3. **Use existing spec** — pass `--spec` if you already have an OpenSpec change for this. Skips this gate and loads that change's proposal/specs/tasks directly.
      >
      > Select (1/2/3):

      - On **1**: Stop plan. Tell the user to use the openspec CLI directly to create a change (run `openspec --help` for available commands). In plan mode, call ExitPlanMode first.
      - On **2** (recommended default — implements R63): Do NOT prompt the user further at this gate (R22 single-touchpoint). Set `openspecInlineGenerate = true`. `openspecContext` stays empty, so OpenSpec enrichment, Gate A, and openspec-task annotations do not activate — artifact authoring is deferred to Step 4 (OpenSpec Appendix generation), where exploration and decomposition data are available. Skip the rest of the OpenSpec block (steps 3–6 — there is no on-disk change to load). Continue with standard planning.
      - On **3**: Re-run the OpenSpec loading logic (steps 3–6) to resolve and load the active change.
3. If the user provided a spec file path pointing into `openspec/changes/<name>/`, extract `<name>` as the active change.
4. Otherwise, Glob `openspec/changes/*/proposal.md` (exclude `archive/`). If exactly one non-archived change exists, use it. If multiple, try matching change directory names against the current git branch name. If still ambiguous, use AskUserQuestion. Context: this plan needs to attach to exactly one OpenSpec change's proposal/specs/tasks — picking the wrong one plans against the wrong requirements.
   > Multiple active OpenSpec changes found. Which one are you working on?
   List each change as an option labeled with its directory name plus a one-sentence description drawn from that change's `proposal.md` (its title or opening summary line) — not the bare name alone, so the user can tell them apart without opening each file.
5. Once the active change is identified, Read in parallel:
   - `openspec/changes/<name>/proposal.md` — intent and scope
   - `openspec/changes/<name>/design.md` — technical approach (may not exist yet; skip if absent)
   - All files matching `openspec/changes/<name>/specs/*.md` — delta specs (the requirements)
   - `openspec/changes/<name>/tasks.md` — OpenSpec's task checklist (may not exist; skip if absent)
6. Store these as `openspecContext` for use in Steps 1–5. Update the plan file header `**Source:**` to `openspec/changes/<name>/`.

**Complexity routing:**

| Scope Signal | Normal Mode | Plan Mode |
|---|---|---|
| 1 file, clear change | Stop — no plan needed. Tell the user. | Lightweight plan (user explicitly chose to plan) |
| 2–3 files, clear scope | Lightweight: skip exploration and review loop | Lightweight |
| 4+ files or unclear scope | Full pipeline (Steps 1–7) | Full pipeline |
| Multiple independent subsystems | Decompose into separate plans | Decompose |

**TodoWrite setup (full pipeline only):** Create TodoWrite items for Steps 1–7. Skip TodoWrite for lightweight plans.

**Session recovery (full pipeline only):** When the designated plan file already has content, restart and overwrite — do NOT prompt (implements R23 single-touchpoint default for Step 0). Clear the file in-place and begin fresh. If the user wants to preserve the prior draft, they can `cp` the file before invoking the skill.

**Context detection and guardrail loading:**

Call `plan_prepare({ skipConfigCheck: <bool>, fromOpenspec: <name or omit> })`. Pass `fromOpenspec` only when `--from-openspec <name>` was passed to plan. The tool call returns the prepare payload directly — there is no output file to read and no cleanup trap to install for this step (that differs from the `explorePack` tempdir, handled separately in Step 1). The tool has already written the `skillInvoked` planIntegrity marker as a side effect; do not call `plan_mark({marker:"skillInvoked"})` — that would be a redundant fourth explicit call, since the marker enum's fourth value is written for free inside `plan_prepare`.

If the call errors, print the errors and stop. Otherwise print the context detection summary from the returned payload:
```
Context detection (from plan_prepare):
  OpenSpec:          [detected, N active changes | not present]
  Branch match:      [yes (<name>) | no]
  --from-openspec:   [valid, N delta specs, tasks.md present | not passed | invalid: <error>]
  Guardrails:        N loaded (N error, N warning)
```

Extract `guardrails` from the output → store as `activeGuardrails`. If the array is non-empty, print: "Loaded N plan guardrails." If empty: "No plan guardrails configured."

**Template resolution and plan initialization (implements R61):** After the context-detection `plan_prepare` call, the gate check, `--from-openspec` handling, and complexity routing (which determine `fromOpenspecDirect`, `lightweight`, and `fileCount`), call `plan_prepare({..., resolveTemplate: true, fromOpenspecDirect, openspecInlineGenerate, lightweight, fileCount})` — pass the same `skipConfigCheck` / `fromOpenspec` fields as the context-detection call. The tool resolves the active template (project override or shipped default), parses sections/conditions, builds the skeleton with conditional and lightweight-adjusted placeholders, and computes complexity routing — all server-side. On error the tool falls back to the shipped default or returns an error; handle errors the same way as the context-detection call.

From the output, write `template.headerMarkdown` + `template.skeletonMarkdown` to the plan file (document header + full section skeleton). Read `template.routing.pipelineMode` to select the pipeline branch (`full` / `lightweight` / `skip`) — the routing table above still applies for the "Stop — no plan needed" and "Decompose" LLM decisions the tool does not make. Store `template.activeTemplatePath` as `activeTemplatePath` for Step 3 lane dispatch (`{PLAN_TEMPLATE_PATH}`) and Step 6.6 format validation (`TEMPLATE_PATH`). Store `template.discoveryQuestions` for Step 1 structured discovery (falls back to built-in scope/integration/success questions when empty) and `template.verificationPatterns` for Step 2 task authoring (falls back to generic verification judgment when empty). Extract `style` (with `narrativeRules`) and `tasks` (with `requiredFields`, `contractShape`) from the output — these are top-level fields populated regardless of `resolveTemplate`, used in Steps 2, 4, and 5.

**Contradictory-signal override (implements R16):** After reading the prepare output, IF `openspec.authoritative.path` is set AND the current session-start `<system-reminder>` contains a line matching `/openspec.*not initialized|not initialized.*openspec/i`, print exactly one line:
`Ignoring contradictory 'not initialized' signal in session context — openspec/config.yaml exists (authoritative source: SDLC's own check via plan_prepare output).`
Then continue the flow. If the contradictory phrase is absent, emit nothing.

**`--from-openspec` handling (after prepare output, before gate check):**

If `fromOpenspec.valid` is true in the prepare output:
1. Read in parallel: `openspec/changes/<name>/proposal.md`, `openspec/changes/<name>/design.md` (optional), all `openspec/changes/<name>/specs/*.md`, `openspec/changes/<name>/tasks.md` (optional)
2. Store as `openspecContext`. Set `fromOpenspecDirect = true`
3. Skip the gate check and complexity routing; still perform the template-resolution `plan_prepare` call above (passing `fromOpenspecDirect: true`) before proceeding to Step 1

If `fromOpenspec` is present but `valid` is false and errors exist: display errors and stop.

**Gate check enhancement:** When no `--from-openspec` but prepare output shows `openspec.branchMatch` with a matching change at stage `ready-for-plan`, update the existing gate check Option 3 text:
> 3. **Use existing spec** — re-invoke with `/plan --from-openspec <matched-change-name>`

**Normal mode path resolution:** Resolve the output path before writing:
1. User-specified path (if provided in conversation)
2. Project `.claude/settings.json` → `plansDirectory` (relative paths resolve from workspace root)
3. Global `~/.claude/settings.json` → `plansDirectory`
4. Default fallback: `~/.claude/plans/`

Naming convention: `YYYY-MM-DD-<feature-name>.md`. Create the directory if needed.

**Plan mode:** Write to the designated plan file path. Skip path resolution.

**planFile marker (implements R20; consumed by `internal/hooks/stop_hooks.go` per R21):** After path resolution, record the resolved plan path in the plan integrity state. Run in both plan-mode and normal-mode branches. Errors are swallowed — marker writes must not block plan creation.

**State-file lifecycle (R20 Lifecycle, fixes #334):** The plan state file follows three rules that callers do NOT need to implement directly — they are enforced inside `plan_prepare`/`plan_mark` (backed by the `internal/state` package) and `internal/hooks/stop_hooks.go` (dispatched via `sdlc-launcher.sh hook stop-plan-integrity`):
- **Prune-on-write** — `plan_prepare` prunes pre-existing `plan-<branchSlug>-*.json` files for the current branch before writing the new state file, so at most one marker per branch exists between plan invocations. `plan_mark` does NOT prune (it would unlink its own target).
- **Consume-then-delete** — the Stop hook reads `planIntegrity` markers, evaluates the gates, then unlinks the marker regardless of outcome (single-shot semantics). Subsequent Stop events on the same branch fall through to the transcript-fallback path — this is correct R21 behavior.
- **GC orphan sweep** — `ship --gc` and `execute --gc` sweep stale `plan-*` markers (TTL-expired or branch-deleted) alongside `ship-*` and `execute-*` files; the JSON output includes a `plan` bucket alongside `ship` and `execute`.

Call `plan_mark({ marker: "plan-file", path: <resolved-plan-path> })` — writes the `planIntegrity` marker consumed by the `stop-plan-integrity` Stop hook.

Replace `<resolved-plan-path>` with the actual absolute path: in plan mode it is the designated plan file path extracted at the top of Step 0; in normal mode it is the path resolved above (from `plansDirectory` or the default fallback). Errors from this call are swallowed — marker writes must not block plan creation.

## Step 1 (CONSUME): Requirements Discovery and Exploration

**`fromOpenspecDirect` enrichment:** When `fromOpenspecDirect` is true (set by `--from-openspec` handling in Step 0):
- Use `tasks.md` as the PRIMARY decomposition skeleton — OpenSpec tasks were deliberately authored
- Skip the "Structured discovery" AskUserQuestion below — the proposal and delta specs already provide scope, integration, and success criteria
- Delta specs remain the authoritative requirements for Step 3 coverage validation

**Discovery dispatch (full pipeline only, implements R24–R28):**

After the `fromOpenspecDirect` enrichment block, determine which exploration path to take.

**No bash trap for this step.** `explorePack.manifestPath` and `explorePack.outDir` are plain fields returned by the `plan_prepare` tool call, not a subshell result — there is nothing to attach a `trap` to. Delete the tempdir explicitly with `rm -rf "<explorePack.outDir>"` at every stop point below (success, brief-validation failure, error fallback); when `explorePack.outDir` is null there is nothing to delete.

- **Full pipeline** (`explorePack.manifestPath` is non-null AND scope is 4+ files / unclear scope):

  1. **Load manifest.** `Read` the JSON file at `explorePack.manifestPath` into context — it is small and bounded (`exploreManifest{version, timestamp, projectRoot, fromOpenspec, userPromptLength, webResearchSignal, scopeHintCount, scopeHintFiles (≤30), skillRegistry (≤12), recentPlans (≤20), outDir}`). Extract `webResearchSignal`, `scopeHintCount`, `scopeHintFiles`, `outDir`. `skillRegistry` and `recentPlans` are context only — use them to sanity-check dimension names against sibling-skill conventions and avoid re-treading recently planned ground.

     **`USER_PROMPT` (the verbatim user request) is authoritative, not the manifest's `webResearchSignal` alone.** The `explorePack` embedded inside `plan_prepare` always runs with an empty prompt — only the standalone `plan_explore_prepare` tool exercises live keyword/web-research detection — so `webResearchSignal` here is a best-effort, possibly-degraded hint. Re-derive web/hybrid need directly from the user's own words in the SCOPE step below; a `false` manifest signal does not override a novel external technology the user actually named.

  2. **SCOPE — derive 3–7 task-specific dimensions (main-session LLM judgment; no tool call).** Based on `USER_PROMPT`, `scopeHintFiles`, and `OPENSPEC_CONTEXT` (the OpenSpec delta-spec paths from Step 0, or "none"), derive dimensions as a JSON array:

     ```json
     [
       {
         "name": "kebab-case-task-specific-name",
         "description": "One sentence describing what this dimension explores",
         "files": ["path/relative/to/project/root.ts"],
         "mode": "code | web | hybrid",
         "model": "haiku | sonnet | opus"
       }
     ]
     ```

     **Name constraints:** task-shaped (`auth-middleware-integration`, `cli-flag-parser-refactor`), never a bare generic axis (`architecture`, `tests`, `security`, `performance`, `documentation`).

     **Mode assignment:** `code` — pure internal analysis (existing code, patterns, file:line evidence); `web` — best-practice research, external technology guidance; `hybrid` — needs both (e.g., "is our oauth2 implementation aligned with RFC 6749?").

     **Web/hybrid requirements:** MUST include ≥1 `web`/`hybrid` dimension when `webResearchSignal: true` OR `USER_PROMPT` names a novel external technology not present in the codebase. MUST NOT include any `web`/`hybrid` dimension for a pure internal refactor (rename/move/dead-code removal) with `webResearchSignal: false`.

     **Model assignment:** `haiku` — fast surface scan; `sonnet` — moderate cross-file judgment; `opus` — complex architectural analysis.

     **Files array:** populated from `scopeHintFiles` plus your own judgment of relevance per dimension. Empty array is valid for exploratory (web) dimensions.

  3. **FAN-OUT.** Mint this run's ledger namespace, then dispatch every dimension in a single message:

     ```
     runId := "plan-explore-" + sanitize(manifest.timestamp)
     ```
     `sanitize` replaces every character outside `[A-Za-z0-9_-]` with `-` (the manifest timestamp is RFC3339 and contains `:`, unsafe for a ledger path segment).

     For each dimension:
     ```
     workerId := slugify(dimension.name)
     ```
     `slugify` lowercases the name, then collapses every run of characters outside `[A-Za-z0-9_-]` to a single `-`.

     Build each dimension's agent prompt from the matching per-mode body below, with the Coordination block appended to every mode:

     For `code` dimensions:
     ```
     You are exploring the codebase for the dimension: {dimension.name}
     {dimension.description}

     Focus on files: {dimension.files or "any relevant files"}
     Project root: {PROJECT_ROOT}

     Tools available: Read, Glob, Grep, Bash (read-only git commands only)
     Do NOT use WebSearch or WebFetch.

     For each finding, cite the specific file:line location.
     Report findings under your assigned ID prefix: F-{dimension.name}-<n>

     F-{dimension.name}-1: path/to/file.ts:42 — <observation>
     F-{dimension.name}-2: path/to/other.ts:88 — <observation>
     ...

     If no findings, return: ZERO_FINDINGS
     ```

     For `web` dimensions:
     ```
     You are researching best practices for the dimension: {dimension.name}
     {dimension.description}

     Context: this research is for planning a software change: {USER_PROMPT}
     Budget: ≤5 WebSearch calls + ≤8 WebFetch calls. Stay within budget.

     Source quality steer: prefer OWASP, RFC, MDN, official vendor docs. Down-weight sources older than 3 years.

     Tools available: WebSearch, WebFetch
     Do NOT use Read, Glob, Grep, or Bash.

     Report findings under your assigned ID prefix: F-{dimension.name}-<n>
     Format: F-{dimension.name}-n: <url> — <observation> (recency: YYYY, source-type: RFC|OWASP|MDN|vendor|blog)

     If no useful findings, return: ZERO_FINDINGS
     ```

     For `hybrid` dimensions:
     ```
     You are exploring both the codebase and external references for the dimension: {dimension.name}
     {dimension.description}

     Focus on files: {dimension.files or "any relevant files"}
     Project root: {PROJECT_ROOT}
     Budget: ≤3 WebSearch calls + ≤5 WebFetch calls. Stay within budget.

     Tools available: Read, Glob, Grep, Bash (read-only git commands only), WebSearch, WebFetch

     Tag each finding:
     - [web-only] — found only in external research, not verified in codebase
     - [verified-in-codebase] — external finding confirmed by codebase evidence
     - [conflicts-with-codebase] — external recommendation contradicts current codebase approach

     Report findings under your assigned ID prefix: F-{dimension.name}-<n>
     Code finding format: F-{dimension.name}-n: path/to/file.ts:42 — <observation>
     Web finding format: F-{dimension.name}-n: <url> — <observation> (recency: YYYY, source-type: RFC|OWASP|...) [tag]

     If no findings, return: ZERO_FINDINGS
     ```

     **Coordination (append to every mode's prompt — do this in this order):**
     ```
     1. Call execute_state({ action: "ledger_checkin", runId: "{runId}", workerId: "{workerId}" }) BEFORE starting exploration.
     2. Explore per the instructions above.
     3. Write your findings to the file ".sdlc-v2/execution/ledger/{runId}/{workerId}.findings.md" as the raw F-{dimension.name}-n text block above, or the literal text ZERO_FINDINGS. Do this BEFORE the next step.
     4. Call execute_state({ action: "ledger_checkout", runId: "{runId}", workerId: "{workerId}" }) LAST, even when your findings file says ZERO_FINDINGS.
     ```

     Dispatch one Agent per dimension, **all in a single message**, with `run_in_background: true`, `subagent_type: general-purpose`, `model: dimension.model`. **Do NOT pass `isolation: "worktree"` or any `isolation` value** (forbidden per issues #370/#372).

     **Workflow variant:** Prefer the Workflow tool's native fan-out when available; otherwise use the flat background-dispatch + ledger path described above.

  4. **POLL.** Loop calling `execute_state({ action: "ledger_status", runId, timeoutSeconds: 1800 })` roughly every 60 seconds until every dispatched `workerId` shows `status: "done"`.

     **Stall handling (fail-partial-open, disclosed):** a `workerId` appearing in `stalledWorkers` is not yet failed — wait one more poll cycle. If it is **still** stalled on the next poll, stop waiting on it: proceed to CRITIQUE with the results collected so far, and explicitly name the skipped dimension(s) in `discovery-brief.md`'s `## Zero-Finding Dimensions` section with the note "skipped — worker stalled twice; no findings collected" — a disclosed degraded mode, not a silent drop.

  5. **CRITIQUE.** Once every dispatched worker is `done` (or force-progressed past a stall above), read each worker's findings file at `.sdlc-v2/execution/ledger/{runId}/{workerId}.findings.md`:
     - **Deduplicate** — same file:line or same URL; keep the most specific observation.
     - **Severity consolidation** — same issue at different severities; keep the highest.
     - **Zero-finding dimensions** — list honestly; never fabricate findings for these.
     - **Contradiction detection** — flag findings that recommend different approaches for the same location.
     - **Web-vs-codebase conflicts** — for hybrid dimensions, flag every `[conflicts-with-codebase]` finding as a high-priority Key Decision candidate.

  6. **CONSOLIDATE.** `Write` `discovery-brief.md` to `{outDir}/discovery-brief.md`:

     ```markdown
     # Discovery Brief

     Generated: <ISO timestamp>
     Dimensions: <n> (<code-count> code, <web-count> web, <hybrid-count> hybrid)
     Scope-hint files analyzed: <scopeHintCount>

     ## Dimensions

     | Dimension | Mode | Model | Findings | Status |
     |---|---|---|---|---|
     | <name> | <mode> | <model> | <count> | ACTIVE \| ZERO_FINDINGS |

     ## Findings

     ### F-<dimension-name>-* (code)

     F-<dimension-name>-1: path/to/file.ts:42 — <observation>

     ### F-<dimension-name>-* (web)

     F-<dimension-name>-1: <url> — <observation> (recency: YYYY, source-type: RFC)

     ### F-<dimension-name>-* (hybrid)

     F-<dimension-name>-1: path/to/file.ts:42 — <observation> [verified-in-codebase]

     ## Contradictions

     <list, or "None detected.">

     ## Zero-Finding Dimensions

     <list, or "None.">
     ```

     When any web/hybrid dimension ran, append:
     ```markdown
     ## Best-Practice Synthesis

     For each web/hybrid finding, state a clear recommendation that the plan author must explicitly ADOPT, REJECT-with-rationale, or mark NOT-APPLICABLE in Key Decisions.

     - F-<dim>-<n>: RECOMMENDATION — <one-sentence actionable recommendation>
     ```

  7. **Read the brief** (`{outDir}/discovery-brief.md`) into context. It is the source of truth for Step 2 task provenance.

  8. **Brief validation:** grep the brief's content for the pattern `F-[A-Z0-9_-]+-[0-9]+`. If zero matches are found, treat discovery as if it had failed: append one line to `.sdlc-v2/learnings/log.md`: `## <YYYY-MM-DD> — plan discovery returned brief without F-DIM-N findings; using fallback inline exploration`, delete the tempdir and ledger directory (`rm -rf "<outDir>"`, `rm -rf ".sdlc-v2/execution/ledger/<runId>"`), then proceed via the **Error fallback** path below. Rationale: a brief with no findings cannot satisfy G15 (Brief citation coverage) and would force every task into "out-of-scope addition" — better to fall back cleanly.

  9. **Cleanup.** On successful brief validation, `rm -rf "<outDir>"` and `rm -rf ".sdlc-v2/execution/ledger/<runId>"` — the brief content is already loaded into context (step 7); nothing further reads the tempdir or the ledger directory.

  **Brief consumption (when brief is present AND validation passed):**
  - Step 2 tasks MUST cite at least one `F-<DIM>-<n>` finding ID from the brief OR be explicitly marked "out-of-scope addition" with rationale (implements R27)
  - When the brief contains a `## Best-Practice Synthesis` section: Key Decisions MUST explicitly ADOPT / REJECT-with-rationale / mark NOT-APPLICABLE each web finding by its `F-<DIM>-<n>` ID (implements R27)

- **Lightweight scope** (`explorePack.scopeHintCount` ≤ 3 OR `explorePack.manifestPath` is null due to lightweight scope):
  - Skip discovery dispatch. Use inline exploration below. No brief. If `explorePack.outDir` is non-null (the prepare tool still created a tempdir), `rm -rf "<explorePack.outDir>"` — nothing was written into it.
  - Issue all Glob/Grep/Read calls for inline exploration in a SINGLE message (parallel dispatch). (implements R37, Fixes #418)

- **Error fallback** (`explorePack.error` is non-null, or brief validation found zero `F-<DIM>-<n>` IDs):
  - Append one line to `.sdlc-v2/learnings/log.md`: `## <YYYY-MM-DD> — plan discovery skipped: <explorePack.error or "brief without F-DIM-N findings">`
  - Use inline exploration below. Plan still produced. (implements R28)
  - Issue all Glob/Grep/Read calls for inline exploration in a SINGLE message (parallel dispatch). (implements R37, Fixes #418)

**Structured discovery:** When requirements are vague (a single sentence or ambiguous goal), OR when multiple materially different approaches exist for a non-trivial aspect, use AskUserQuestion with targeted questions. Concrete triggers for the latter: (a) exploration surfaces two or more viable architecture patterns for the same aspect; (b) an ambiguous scope boundary exists where including vs. excluding a component would materially change the plan; (c) the codebase sends conflicting signals — two existing patterns either of which could reasonably be followed. Frame each question per the decision-framing rule, scaled to `style.audience` (resolved by the `plan_prepare` call in Step 0): lead each question with one sentence of context on what it decides and why it matters before asking it. When `template.discoveryQuestions` from the `plan_prepare` output is non-empty, use those questions verbatim — the question text itself is not modified — but still lead each with the one-sentence context per the decision-framing rule. When the list is empty (project override omitted discovery questions), fall back to:
1. **Scope** — what's in, what's explicitly out? Context: unresolved scope boundaries turn into missing or over-built tasks in Step 2.
2. **Integration** — what existing code does this touch? Context: decides which files land in task `Files:` blocks and which existing patterns the tasks follow.
3. **Success** — how will we know it works? Context: becomes each task's `Verify:` field, so a vague answer here produces untestable tasks.

Wait for answers before continuing.

**`--auto` suppression (R65):** When `--auto` is set, suppress the AskUserQuestion above. Pick the most conservative approach autonomously: for approach-pattern ambiguities (triggers a/c), prefer whichever viable option most closely matches existing codebase patterns as a tiebreaker between equally-conservative choices; for scope-boundary or vague-requirement ambiguities (trigger b, or an underspecified goal), default to the narrowest scope that still satisfies the stated requirement. Record the choice in `## Key Decisions` with rationale, and add a `## Deviations & assumptions` row with `asked=no`.

**Codebase exploration (skip for lightweight):** Use read-only tools (Glob, Grep, Read, LSP):
- Relevant file structure and patterns in affected areas
- Existing modules, interfaces, and types the feature touches
- Testing patterns used in the project
- Build/lint/test commands (from Makefile, package.json, or similar)
- Naming conventions and code style
- All Glob/Grep/Read calls above MUST be issued in a SINGLE message as parallel tool calls — mirror the in-skill precedent at the OpenSpec artifact reads (`Read in parallel: …`). (implements R37, Fixes #418)

Identify constraints: language, framework, existing conventions, testing approach.

**OpenSpec enrichment (when `openspecContext` is available):**
- Use `proposal.md` for goal and scope understanding (what's in, what's out)
- Use delta specs (`specs/*.md`) with their ADDED/MODIFIED/REMOVED sections as the authoritative requirements — each delta entry is a requirement
- Use `design.md` for architecture constraints and technical approach decisions
- Use `tasks.md` as a coarse reference for decomposition — OpenSpec tasks are higher-level than plan tasks, so decompose further rather than copying verbatim
- When the OpenSpec artifacts provide sufficient scope, integration, and success criteria, skip the "Structured discovery" AskUserQuestion — the proposal and delta specs already answer those questions

**Write to plan file:** After exploration, update the plan file:
- Fill in Goal, Architecture, Verification header fields
- Write the `## Context` section — answers to the Discovery Questions in plain language (R62)
- Write the `## Research Findings` section — captures exploration output: file patterns, existing modules, naming conventions, testing patterns, and build/lint/test commands discovered during codebase exploration. This section persists in the final plan (template-required, narrative).
- Append a `## Requirements` section with numbered checklist (one bullet per requirement) — this is temporary scaffolding, removed in Step 2 post-write cleanup

**Re-anchor:** Before leaving Step 1, re-read the plan file's Requirements section. This counters attention drift after many exploration calls.

**Gate A — Intake Audit (implements R39 — Fixes #445):**

When `openspecContext.requirements` is present (non-null) in the prepare output:

1. Dispatch one Gate A audit Agent using `intakeAuditDispatch` parameters from the prepare output (P20). Source `subagentType`, `model`, and `promptTemplatePath` verbatim from `intakeAuditDispatch` — do NOT hardcode model or template path (`agent-dispatch-script-driven` guardrail). If `intakeAuditDispatch.promptTemplatePath` is null, skip Gate A and emit one note: `Gate A skipped — intake-verify-prompt.md not found.`

   Read the file at `intakeAuditDispatch.promptTemplatePath`; fill the following template variables before dispatching.

2. Fill the prompt template variables:
   - `{PROPOSAL}` — content of `openspec/changes/<name>/proposal.md` (already read in Step 0), or `"[artifact missing]"` if absent
   - `{DELTA_SPECS}` — concatenated content of all `openspec/changes/<name>/specs/*.md` files (already read in Step 0), or `"[artifact missing]"` if none found
   - `{TASKS_MD}` — content of `openspec/changes/<name>/tasks.md` (already read in Step 0), or `"[artifact missing]"` if absent
   - `{DESIGN}` — content of `openspec/changes/<name>/design.md` if present, or `"[artifact missing]"`
   - `{REQUIREMENTS_JSON}` — `JSON.stringify(openspecContext.requirements)` from prepare output, or `"null"` if null

3. Parse the agent's JSON response `{ findings, verdict, skipped }`.

4. Verdict handling:
   - `verdict: "CRITICAL"` — **block decomposition**. Do NOT proceed to Step 2. Surface findings to user with E7 menu: (a) fix source change artifacts and re-run; (b) override (proceed anyway, recording the override in `## Intake Audit Caveats`). No `--auto` bypass for CRITICAL.
   - `verdict: "WARNING"` or `"SUGGESTION"` — append a `## Intake Audit Caveats` section to the plan file listing the findings. Proceed to Step 2.
   - `verdict: "PASS"` — proceed to Step 2 without any caveat section.

5. When `openspecContext` is absent (non-OpenSpec plan), skip Gate A entirely. Emit one note: `Gate A skipped — plan is not OpenSpec-sourced.`

## Step 2 (PLAN): Decompose Into Tasks

**Scope check:** If requirements span independent subsystems with no shared state, use AskUserQuestion:
> These requirements cover independent subsystems. Recommend splitting into N plans. Proceed as one plan or split?

Wait for answer.

**Approach check:** If decomposition reveals multiple viable approaches for a component (e.g., sync vs. async, library vs. hand-rolled), use AskUserQuestion presenting the trade-offs of each option. **Dedup against Step 1:** if this ambiguity was already resolved by a Structured discovery question (or its `--auto` fallback) in Step 1, skip — do not re-ask. Only surface approach questions that are newly revealed by the decomposition breakdown.

Wait for answer.

**`--auto` suppression (R65):** When `--auto` is set, suppress the AskUserQuestion above. Pick the most conservative approach autonomously, using the option that most closely matches existing codebase patterns as a tiebreaker between equally-conservative choices. Record the choice in `## Key Decisions` with rationale, and add a `## Deviations & assumptions` row with `asked=no`.

**File structure mapping** — before writing tasks, map out:
- Files to create (path + one-line responsibility)
- Files to modify (path + what changes)
- Test files (aligned with source files)

**`fromOpenspecDirect` decomposition:** When `fromOpenspecDirect` is true:
- Adopt the task structure from `tasks.md` as the starting skeleton
- For each OpenSpec task: map to one plan task (or split if > 5 files), add Complexity/Risk/Depends on/Verify metadata, and expand the description to be self-contained for agent dispatch
- If `tasks.md` is absent (prepare output shows `hasTasks: false`), fall back to standard decomposition from delta specs below

**OpenSpec task annotation (implements R29 — Fixes #414):** When `fromOpenspecDirect` is true AND `openspecContext.tasks[]` is non-null in the prepare output, each plan task derived from one or more OpenSpec tasks MUST carry an `openspec-task:` block beneath its standard metadata fields. Format documented in `./plan-format-reference.md`:

```
**openspec-task:**
- change: <change-name>
- ref: <kebab-slug-6char-hash from openspecContext.tasks[i].ref>
- line: <openspecContext.tasks[i].line>
- title: <openspecContext.tasks[i].title>
```

Plan tasks NOT derived from any OpenSpec task MUST omit the field. N:1 mapping (multiple plan tasks → same `ref`) is allowed when one OpenSpec task expands into several plan tasks — copy the same `change`/`ref`/`line`/`title` quad on each.

**Out-of-scope OpenSpec tasks (implements R30):** When the plan introduces a plan-only task with no OpenSpec source, the plan-author MUST also append (or extend) an `## Out-of-scope OpenSpec tasks` section listing each uncovered OpenSpec task title with a one-line rationale — OR add at least one plan task carrying that `ref`. Every entry from `openspecContext.tasks[]` must be either covered by ≥1 plan task's `openspec-task.ref` OR listed in `## Out-of-scope OpenSpec tasks`. This is enforced by G16 in Step 3.

**Task decomposition rules:**
- Each task = one independently completable unit with a clear deliverable
- Each task touches 1–5 files (more than 5 → split)
- Order: foundations → features → integration → polish
- Dependencies explicit (task B names task A if it needs A's output)

**Wave-Optimization Discipline:** `execute` runs tasks in dependency-ordered waves — every task with
no unresolved dependency in a wave runs in parallel. Each unnecessary `Depends on` edge pushes a task
into a later wave for no real reason, so maximize parallelism by minimizing false dependencies:
- Default to `Depends on: none`. Only declare `Depends on: Task X` when task B truly cannot start
  until task A finishes — it reads a file A produces, or extends a struct/interface A defines. Name
  the concrete artifact the dependency is on; if you can't name one, there is no dependency.
- "Related to" is not "depends on." Two tasks that modify the same package but touch different
  files, or that approach the same feature from different angles, can still run in the same wave.
- When a task blocks many downstream tasks, check whether all of them need the *whole* task or just
  part of it. If task X blocks 5 downstream tasks but only 2 of them actually need the struct or
  interface X defines, split X into a struct/interface-only task (fast, unblocks all 5 immediately)
  and a full-implementation task (slower, only the 2 that need the real logic depend on it).

**OpenSpec-aware decomposition (when `openspecContext` is available):**
- Map each ADDED and MODIFIED requirement from the delta specs to at least one task
- In the Key Decisions section, note which OpenSpec `design.md` decisions were adopted and which (if any) were overridden with rationale
- Set the plan header `**Source:**` field to `openspec/changes/<name>/` (not "conversation context")

**Key decisions:** Note every decision where you chose between valid approaches. Focus on choices where a reasonable implementer might differ without the rationale. Skip obvious decisions.

**Per-task metadata (required, consumed by execute):** Read `./plan-format-reference.md` first and match its worked examples:

```markdown
### Task N: [Component Name]

**Complexity:** Trivial | Standard | Complex
**Risk:** Low | Medium | High
**Depends on:** Task X, Task Y (or "none")
**Verify:** tests | build | lint | manual

**Files:**
- Create: `exact/path/to/file.ts`
- Modify: `exact/path/to/existing.ts` — [what changes]
- Test: `tests/exact/path/to/test.ts`

**Notes:** (optional — rationale only, ≤5 lines; the "what" lives in Files/Contract/Acceptance)
[Why this approach, non-obvious constraints, or assumptions. The executable shape lives in
Files + Contract + Acceptance criteria — do not restate it here.]

**Acceptance criteria:**
- [ ] [Specific, verifiable criterion]
- [ ] [Another criterion]

**Contract:**
- shape (<code|docs|openspec>): [the type-aware decided shape execution renders verbatim]
- names: [exact symbols / IDs / headings / fields]
- mirror: [existing artifact + line anchors to copy structure from]
- decisions: [per-task decided choices bound to this deliverable]
- sync: [sibling artifacts that must stay byte-consistent]
- example: [optional — concrete input/output pair when shape's prose leaves a genuinely ambiguous boundary; omit when a render-trigger surface applies, since the render IS the example]
```

**Contract block (required — implements R45):** Every artifact-touching task MUST include a `**Contract:**` block per `./plan-format-reference.md`, carrying the type-appropriate decided shape (code: signatures/types/flags/error-cases/import-paths; docs: template+sections+audience+cross-links; openspec/spec: requirement IDs ADD/MODIFY/REMOVE + delta text + numbering). The plan type is derived from the task's `Files:` paths; a mixed-artifact task uses its dominant artifact's column. A task whose Contract is absent or merely restates "update X to do Y" is flagged by G18 in Step 3.

**Contract requires a visual artifact — prose-only fails G18:** The Contract's `shape` MUST render at least one visual artifact, not describe it in prose alone: a fenced code block for a struct/type definition, a field-diff table (`+`add/`−`remove) for a modification, a before/after render for a workflow change, or a JSON/YAML schema snippet for a config change. A `shape` with no fenced block, table, or diff is insufficient even when the prose is concrete. This is distinct from `## Concrete Artifacts (render don't narrate)` in `./plan-format-reference.md`: the Contract's visual renders the deliverable's **decided shape** (what execution implements verbatim); Concrete Artifacts render **observable** payloads/state/outcomes (what a reviewer verifies once built) — the two can reuse the same snippet when they coincide, but are judged separately.

**G18 — Settlement / contract concreteness (error-severity):** Flags any artifact-touching task whose `Contract:` is absent or merely restates "update X to do Y" without a concrete type-appropriate shape. Owned by the content-coverage lane. Blocks plan approval until the Contract pins the decided shape. Backed by deterministic presence floor PF7 (`validate({action:"plan_format"})`, default check set); LLM G18 judges concreteness above the floor.

**Render don't narrate (surface-conditional — implements R46):** When a task touches a concrete-artifact surface (payload, struct/schema field change, status enum, flow, config/flag delta, error mode, data-writing end-state), RENDER the artifact (fenced block / table / before→after diff) — do not describe it in prose. Use the catalog + conventions in ./plan-format-reference.md. Cap: one elided (…) example per distinct contract shape (a distinct contract shape is one unique combination of method + path for REST, or flag + type for CLI — two endpoints with the same method but different paths are distinct shapes). Trivial docs/rename tasks render nothing. (Mermaid fenced blocks allowed for flow/call-order/state surfaces; no MDX.) A task whose concrete-artifact surface is described in prose rather than rendered is flagged by G19 in Step 3.

**G19 — Render-don't-narrate (error-severity):** For each render-trigger surface (REST/RPC endpoint, CLI flag, schema field, status enum, state/flow/enum, config delta, error taxonomy) a task touches, flags that surface if its shape is narrated in prose instead of rendered as a fenced block, table, or before→after diff — a render for one surface does NOT excuse prose for another surface in the same task. Owned by the content-coverage lane. Blocks plan approval until every touched surface is rendered. Not-applicable for trivial tasks and pure docs/rename tasks. **Template-driven exemption (R61):** content under a section name marked `<!-- narrative: true -->` in the active template is exempt — the exempt set comes entirely from the template's markers, not from hardcoded section names. LLM-only (semantic — render-trigger detection); hardened by prose. No deterministic floor (KD2).

**G20 — Notes rationale-only (error-severity):** Flags a `Notes:` block that restates the task's Contract/acceptance instead of carrying only rationale. Owned by the content-coverage lane (lanes[1]). Blocks plan approval until the `Notes:` block is rationale-only. **Template-driven exemption (R61):** content under a section name marked `<!-- narrative: true -->` in the active template is exempt.

**G21 — Self-contained code refs (error-severity):** Flags a bare `file:line` change reference not anchored with surrounding lines (or the full function body) plus an inline diff. A `file:line` used as a pointer / `Contract.mirror` precedent anchor is exempt. Owned by the content-coverage lane (lanes[1]). Blocks plan approval until the reference is self-contained. LLM-only (semantic — change-site vs precedent ref); no deterministic floor (KD6).

**Verification strategy — match to task type.** When the active template provides `## Verification Patterns`, draw `**Verify:**` values from those patterns. When the template has no Verification Patterns (project override omitted it), use these defaults:
- Feature/logic → TDD (write failing test, implement, pass)
- Config/infrastructure → build verification
- Documentation → manual review
- Integration → integration test or E2E

**Test Isolation for Parallel Execution:** When authoring `Verify:` for a task likely to share a wave
with other tasks touching the same package, add a scope hint in parentheses naming the exact command
to run instead of the full suite — e.g. `Verify: tests (go test ./internal/tools/ -run TestFoo)`. This
stops N parallel agents in the same wave from each re-running the full suite; the full suite still
runs once at the wave boundary as the gate check regardless of any per-task scope hints. See
`./plan-format-reference.md`'s `## Verify Field — Scoped Hints` and `## Isolation` sections for the
full syntax and when to use it.

**Write to plan file — template-required sections and tasks:** Write ALL sections declared in the active template's `## Required Sections` list, in the order defined by `./plan-format-reference.md`'s `## Section Order`. Do NOT hardcode section names — the template is the single source of truth. For each template-required section:

- `## Context` — already written in Step 1 (answers to Discovery Questions); update if Step 2 research expanded the picture
- `## Research Findings` — already written in Step 1 (exploration output); update if needed
- `## Deviations & assumptions` — populate the table (Item | asked | does | why): one row per place the plan diverges from or assumes beyond the literal request. **`asked` column semantics:** `asked=yes` when AskUserQuestion was actually used to resolve the item; `asked=no` when the decision was made autonomously (no ambiguity worth surfacing) or when AskUserQuestion was suppressed by `--auto`. When the plan matches the request exactly, replace the placeholder row with a single "none" row (implements R47; presence enforced by PF6/PF10 via the active template). **De-dup rationale convention (implements R52):** Each decision gets one rationale entry.
- `## Key Decisions` — note every decision where you chose between valid approaches. Focus on choices where a reasonable implementer might differ without the rationale. Skip obvious decisions.
- `## Contract Examples` — one worked `**Contract:**` block per column type (code / docs / openspec) actually used by the plan's tasks, per R60. Reuse the three worked examples from `./plan-format-reference.md` verbatim.
- `## Final Shape` — describe the end-state once all tasks are complete (what the deliverable looks like when done, not how it gets there)
- `## OpenSpec Appendix` — see "OpenSpec Appendix generation" in Step 4/5 below. During Step 2, leave the skeleton placeholder (either `[TBD]` or `Not applicable — no OpenSpec change` per the condition evaluated in Step 0).
- Task blocks — all `### Task N:` blocks with per-task metadata
- Any project-custom sections from the template that are not in the list above — write them with the body format appropriate to their name, or `[TBD]` if the format is unknown

**R62 plain-language style (applies to all narrative sections):** Sections marked `<!-- narrative: true -->` in the template (e.g. Context, Research Findings, Key Decisions, Final Shape) MUST follow these writing rules:
- Short sentences — one idea per sentence
- Every technical term explained inline on first use (e.g., "webhook — an HTTP callback the payment provider calls on our server")
- Goal structured as a multi-line bullet list, not a dense text blob
- Key Decisions readable by non-experts: state the choice, the alternative, and why one wins — prefer plain wording over jargon
- Visual breathing room — blank lines between ideas

R62 is a writing-quality convention judged by the Step 5 lens reviewers (R36) alongside their other checks — no new gate is introduced, and it is not enforced deterministically.

**Post-write cleanup:** Remove the `## Requirements` working section from the plan file. Requirements are traceable through task acceptance criteria; the section was temporary scaffolding.

## Step 3 (CRITIQUE): Self-Review Plan — 5-Lane Parallel Gate Evaluation (R35, Fixes #418)

**Re-anchor:** Re-read the plan file before dispatching lanes. The file — not your memory of it — is the source of truth.

**Fan-out dispatch: Dispatch ALL FIVE Step 3 lanes from `lanes[]` (P16) in a SINGLE message as parallel Agent tool calls. Do not dispatch them sequentially.**

All 21 quality gates (G1–G21) are partitioned across five lanes — each gate belongs to exactly one lane. G20 and G21 are owned by the content-coverage lane (lanes[1]). Lane dispatch parameters (`subagent_type`, `model`, and prompt body read from `promptTemplatePath`) MUST be sourced verbatim from the corresponding `lanes[i]` entry in the prepare output (`agent-dispatch-script-driven` guardrail — do NOT hardcode these values).

For each `lanes[i]` entry (i = 0..4):

- `subagent_type`: `lanes[i].subagentType`
- `model`: `lanes[i].model`
- prompt body: Read `lanes[i].promptTemplatePath` and fill template variables:
  - All lanes: `{PLAN_FILE_PATH}` (absolute path to plan file), `{PROJECT_ROOT}` (cwd)
  - Lanes 0–3 non-G17: `{REQUIREMENTS_SUMMARY}` (the numbered requirements list from Step 1 CONSUME — same content as `{REQUIREMENTS_CHECKLIST}` in Step 5; retained in memory from Step 1), `{ACTIVE_GUARDRAILS}` (from `guardrails[]` P7), `{OPENSPEC_TASKS}` (from `openspecContext.tasks` P13, null when not OpenSpec-sourced), `{BRIEF_FINDING_IDS}` (from `explorePack.manifestPath` context, null when no brief)
  - Lane 1 (content-coverage) additionally: `{FORMAT_REFERENCE_PATH}` — absolute path to plan-format-reference.md (sibling of lane-content-coverage-prompt.md in the same skill directory; resolve as `dirname(lanes[1].promptTemplatePath)/plan-format-reference.md`), `{PLAN_TEMPLATE_PATH}` — `activeTemplatePath` resolved in Step 0 (the absolute path to the active plan template — project override or shipped default)
  - Lane 4 (G17/dimension-coverage): `{DIMENSIONS_DIR}` (`.sdlc-v2/review-dimensions/`), `{COPILOT_DIR}` (`.github/instructions/`), `{GITHUB_HOSTING_DETECTED}` (`githubHosting.detected` from P14), `{LEARNINGS_LOG_PATH}` (`.sdlc-v2/learnings/log.md`), `{PR_COMMIT_WINDOW}` (best-effort "last 14 days" if unknown)

**Null `promptTemplatePath` handling:** When `lanes[i].promptTemplatePath` is null (prepare script reported it could not find the template), skip that lane's dispatch and immediately add a synthetic blocking issue:
```
{ laneStatus: "failed", gateIds: lanes[i].gateIds, issues: [{ gateId: lanes[i].gateIds[0], severity: "error", message: "Lane <name> skipped — promptTemplatePath null (template not found at prepare time)", blocking: true }], passes: [] }
```
Exception: lane 4 (G17/dimension-coverage) — when `lanes[4].promptTemplatePath` is null, treat as empty findings (advisory per R31 dispatch-failure fallback) and continue. Log to `.sdlc-v2/learnings/log.md`:
```
## YYYY-MM-DD — plan: G17 skipped — promptTemplatePath null (template not found at prepare time)
```

**No `isolation: "worktree"` on any lane dispatch** (forbidden per issues #370/#372).

**Collect lane results and merge:**

Each lane returns a JSON object with schema:
```json
{ "gateIds": [...], "issues": [...], "passes": [...], "laneStatus": "ok"|"failed"|"timeout" }
```
Lane 3 (guardrail-compliance) additionally returns `guardrailCompliancePayload` in the JSON object — store this for Step 4's `## Guardrail Compliance` section.
Lane 4 (dimension-coverage/G17) returns the G17 findings JSON — parse the `findings` object and persist as `g17Findings` for Step 4.

**Merge algorithm:** Collect each lane's result (including synthetic `laneStatus: "failed"` entries for null-`promptTemplatePath` lanes) into a `laneResults` array. Call `plan_support({action: "merge_results", laneResults: [...], expectedGates: ["G1".."G21"]})`. Process the returned `allIssues`, `coverageGaps`, and `laneFailures` — the tool handles issue/pass union, gate-coverage checks, lane-failure injection (G17 advisory per R31), and deduplication.

Note every issue from `allIssues`. Do NOT write to the plan file in this step.

**JOIN barrier — `guardrailsEvaluated` (implements R20, R35):** After the guardrail-compliance lane (lanes[3]) result is incorporated into the merged issue list, record the checkpoint by calling `plan_mark({ marker: "guardrailsEvaluated" })` — writes the `planIntegrity` marker consumed by the `stop-plan-integrity` Stop hook. **Do NOT call this before lanes[3] returns.**

**JOIN barrier — `critiqueRan` (implements R20, R35):** After ALL five lanes have returned and the merged issue list is complete (including G17/lanes[4] findings parsed into `g17Findings`), record the checkpoint by calling `plan_mark({ marker: "critiqueRan" })`. **Do NOT call this until all five lanes have returned.** This extends the existing G17 join semantics to every lane.

**Once-per-run checkpoints:** `guardrailsEvaluated` and `critiqueRan` are written exactly once — during the initial Step 3 pass. When Step 3 lanes are re-dispatched via the merged dispatch in Step 5 (see "Material change detection and merged re-dispatch" below), these markers are NOT re-written. The Stop hook already holds the integrity proof from the first pass; re-marking would reset the timestamp without adding information.

## Step 4 (IMPROVE): Revise Plan and Present for Approval

Fix all issues from Step 3. Rewrite the plan file with fixes applied (edit the existing file, don't append). If any revision changes the scope or approach from what was originally recorded, update the `## Deviations & assumptions` table accordingly.

**Task block authoring config:** When revising or adding task blocks, honour `tasks.requiredFields` and `tasks.contractShape` from the `plan_prepare` output. `tasks.requiredFields` lists the metadata fields every task block must carry (enforced by PF11). `tasks.contractShape` describes the expected contract format (enforced by PF12). These are project-configurable — do not hardcode defaults when the config provides values.

**G16 (OpenSpec tasks.md coverage) failure resolution:** When G16 reports uncovered OpenSpec task entries, resolve each one by EITHER (a) adding a plan task with the missing `openspec-task` block carrying the corresponding `ref`, OR (b) appending the uncovered title under the `## Out-of-scope OpenSpec tasks` section with a one-line rationale. Both paths are valid; choose based on whether the implementation actually covers the work.

If `activeGuardrails` is non-empty, append a `## Guardrail Compliance` section to the plan file listing each guardrail's evaluation result. Error-severity failures must be resolved before presenting to user. When an error-severity failure cannot be resolved by plan revision and blocks the workflow, offer **harden** (run `/harden` to analyze why this failed and propose stronger guardrails / dimensions / instructions that would catch it earlier next time — opt-in, no surface is edited without your approval) alongside the user-revision options. When the user selects **harden** (interactive mode only — suppressed when `--auto` is set), dispatch `Skill(harden)` with `--failure-text "Plan blocked by error-severity guardrail <id>: <description> — <rationale>"`, `--skill plan`, `--step "Step 4 — IMPROVE"`, `--operation "error-severity guardrail block"`. Implements R19. Format:

```markdown
## Guardrail Compliance

| Guardrail | Severity | Status | Rationale |
|---|---|---|---|
| no-direct-db-access | error | PASS | No tasks modify database schema files |
| prefer-composition | warning | PASS | No class hierarchies proposed |
```

**Suggested Review Dimensions (R34, KD6 placement — Fixes #417):**

When `g17Findings.findings` is non-empty, append the `g17Findings.rendering` markdown verbatim to the plan file. Placement (KD6):
- If `## Guardrail Compliance` was written above, splice immediately after that section.
- Otherwise, splice immediately after the last `### Task N:` block.

When `g17Findings.findings` is empty (or `g17Findings` is the empty-fallback from a dispatch failure), do nothing. Absent proposals are not a failure — G17 is advisory (R31).

**OpenSpec Appendix generation (implements R59, R63):** Three-way conditional:

**(a)** When `fromOpenspecDirect` is true (full-pipeline OpenSpec path), call:

```
plan_support({ action: "openspec_appendix", changeName: <name>, proposalPath, designPath, specPaths, planTasks })
```

Write the returned `appendixMarkdown` into the `## OpenSpec Appendix` section. The tool builds the requirement inventory table and delta-spec fragments (with nested-fence safety) server-side.

**(b)** When `openspecInlineGenerate` is true (inline generate path from gate check Option 2, implements R63), populate the `## OpenSpec Appendix` section with an **OpenSpec Artifacts (Draft)** label and author fresh artifacts from exploration and decomposition data:

1. **`### Proposal Summary`** — author a proposal summary from the user's request and exploration findings. Wrap with `<!-- openspec-target: proposal.md -->`.
2. **`### Delta Specs`** — author spec deltas with ADDED/MODIFIED/REMOVED sections derived from exploration and decomposition. Wrap with `<!-- openspec-target: specs/<feature-name>.md -->`.
3. **`### Tasks List`** — author a tasks checklist derived from the plan's task decomposition. Wrap with `<!-- openspec-target: tasks.md -->`.

Each fragment MUST be wrapped with `<!-- openspec-target: <path> -->` annotations as shown above. The appendix MUST be complete enough that `openspec create`/`openspec validate` can run directly off it after handoff, with no further interactive authoring step. **Nested-fence safety (N+1 backticks):** Before fencing a fragment, count the longest consecutive backtick run (N) inside its content and wrap in max(N+1, 4) backticks — CommonMark closes a fence only on a run at least as long as the opening. Each fragment is fenced independently.

**(c)** When `fromOpenspecDirect` is false AND `openspecInlineGenerate` is false, the skeleton placeholder from Step 0 already reads `Not applicable — no OpenSpec change` — leave it as-is.

Step 4 is autonomous (implements R22 single-touchpoint handoff). After fixes are applied (Guardrail Compliance section written when `activeGuardrails` is non-empty, and Suggested Review Dimensions spliced when `g17Findings.findings` is non-empty per R34), proceed directly to Step 5. The user does NOT see the plan at Step 4; the single user touchpoint for the finalized plan is Step 7 (Handoff). The Step 4 error-severity guardrail-block harden offer above remains a genuine decision gate and is preserved (R19).

## Step 5 (CRITIQUE): Plan Review Loop — Multi-Lens Fan-Out (R36, Fixes #418)

Skip for lightweight plans (2–3 file scope from Step 0 routing).

**Material change detection and merged re-dispatch (implements R64):**

When `materialChangeDetected` is true (set by the Step 6 IMPROVE pass — see below), dispatch Step 3 lanes AND Step 5 lens reviewers in a SINGLE message as parallel Agent tool calls (`run_in_background: false` on each). This merged dispatch counts as **one** iteration of the existing review loop — the iteration counter increments by 1, not 2, and the max-3 cap (R8, R-c1) fires normally.

- **Dispatch contents:** all five `lanes[]` (P16) + all `lensReviewers[]` (P17, or the single reviewer for <5-task plans). Each agent uses the same dispatch parameters, template variables, and model rules defined in Step 3 (lanes) and Step 5 (lenses) respectively.
- **Await barrier:** do not consolidate or advance until exactly N = (5 lanes + M lenses) results are collected. Never consolidate on partial or zero returns.
- **Merge:** use the combined `plan_support({action: "merge_results", laneResults, lensResults, expectedGates, isRedispatch: true})` call described in the Step 5 merge section below. The tool handles lane/lens merging, gate coverage, G17 advisory demotion, and cross-source deduplication in one call.
- **G17 on re-dispatch (advisory only):** lanes[4]/G17 findings from the re-dispatch merge as advisory only. Step 4 has already run, so there is no `## Suggested Review Dimensions` consumer — do not re-splice G17 findings into the plan file. Persist updated `g17Findings` in memory for scorecard reference only.
- **Guardrail-block gate preservation (R19):** lanes[3] (guardrail-compliance) findings from the re-dispatch are scanned the same way Step 4 scans them. If any error-severity guardrail violation is present in the re-dispatch merge, do NOT route it silently into Step 6's blocking-issue set — surface the same guardrail-block harden offer described in Step 4 (offer **harden** alongside the user-revision options; dispatch `Skill(harden)` with `--failure-text "Plan blocked by error-severity guardrail <id>: <description> — <rationale>"`, `--skill plan`, `--step "Step 5 — merged re-dispatch"`, `--operation "error-severity guardrail block"` only if the user selects harden, suppressed when `--auto` is set) before proceeding with Step 6 fixes. This preserves R19 across the merged re-dispatch path.
- **`guardrailsEvaluated` / `critiqueRan`:** NOT re-written (once-per-run checkpoints — see Step 3).
- **Clear flag:** set `materialChangeDetected = false` after the merged issue set is assembled.

When `materialChangeDetected` is false (or unset), dispatch only the Step 5 lens reviewers as below (normal path).

<!-- fan-out-dispatch: await-barrier-required -->
**For plans with ≥5 tasks — Multi-lens fan-out:** Dispatch ALL lens reviewers from `lensReviewers[]` (P17) in a SINGLE message as parallel Agent tool calls, each with `run_in_background: false` (R-orchestrator-await, #487). Do not dispatch them sequentially. Reuse canonical fan-out wording: "Dispatch ALL … in a SINGLE message as parallel Agent tool calls."

**Await barrier (R-orchestrator-await, R-c1, #487):** do not consolidate lens findings or advance until exactly N lens results are collected (N = lenses dispatched). Never consolidate on partial or zero returns.

For each `lensReviewers[i]` entry (i = 0..2):
- `subagent_type`: `lensReviewers[i].subagentType`
- `model`: override with the **opposite-of-plan-author model** at dispatch time (cross-model property — plan written by sonnet → dispatch reviewer as opus; plan written by opus → dispatch reviewer as sonnet). This overrides the default `lensReviewers[i].model` value from the prepare output for ≥5-task plans.
- prompt body: Read `lensReviewers[i].promptTemplatePath` and fill template variables:
  - `{PLAN_FILE_PATH}` — absolute path to the plan file
  - `{LENS}` — `lensReviewers[i].lens` (one of `architecture`, `requirements`, `risk`)
  - `{LENS_FOCUS}` — `lensReviewers[i].focusCategories` rendered as a bullet list
  - `{REQUIREMENTS_CHECKLIST}` — numbered list from Step 1 (CONSUME)
  - `{SOURCE_REQUIREMENTS}` — file path or inline text of spec (if available)
  - `{BRIEF_FILE}` — absolute path to `discovery-brief.md`, or `"none — orchestrator skipped"`
  - `{OPENSPEC_TASKS}` — serialized JSON from `openspecContext.tasks[]`, or `"none — plan not from OpenSpec"`
  - `{GUARDRAILS}` — one guardrail per line (`- [id] (severity): description`), or `"none configured"`
  - `{REQUIREMENTS_JSON}` — `JSON.stringify(openspecContext.requirements)` when present, or `"null"` (null-safe; lens prompts render `"null"` as `"none — inventory unavailable, use checklist"`)
  - `{NARRATIVE_RULES}` — `style.narrativeRules` from the `plan_prepare` output, joined as a newline-separated list, or `"none configured"` when the array is empty. Threads the project's narrative writing rules into lens reviewer evaluation.

When `lensReviewers[i].promptTemplatePath` is null, skip that lens and log to `.sdlc-v2/learnings/log.md`: `## YYYY-MM-DD — plan: lens "<name>" skipped — promptTemplatePath null (template not found at prepare time)`. Continue with remaining lenses.

**No `isolation: "worktree"` on any lens reviewer dispatch** (forbidden per issues #370/#372).

**Merge lens reviewer results (per iteration):** Collect each lens reviewer's result into a `lensResults` array. Call `plan_support({action: "merge_results", lensResults: [...]})`. Process the returned `mergedStatus` (`Approved` / `Issues Found`), `allIssues`, and `recommendations` — the tool handles status derivation, issue dedup by `(taskRef, message-normalized-prefix)`, and recommendation dedup by string prefix. For the merged re-dispatch path (when `materialChangeDetected` is true), combine both in one call: `plan_support({action: "merge_results", laneResults: [...], lensResults: [...], expectedGates: ["G1".."G21"], isRedispatch: true})` — `isRedispatch` makes G17 findings advisory-only and applies cross-source deduplication.

**Iteration counter**: increment by 1 only after the await barrier above is satisfied (exactly N lens results collected, N = lenses dispatched); never increment on partial or zero returns (R-orchestrator-await, R-c1, #487).

**For plans with <5 tasks — Single reviewer (status quo):** Dispatch one reviewer with `{LENS}=all` using `./plan-reviewer-prompt.md` directly (same model acceptable). Status quo behavior preserved.

**Gate B — Verification Scorecard (implements R40, R42, R44 — Fixes #445):**

After the merge step, assemble the `## Verification Scorecard` section in the plan file. This is purely additive — it MUST NOT remove or alter any existing gate evaluation, `buildLanes`, or the `{G1..G21}` union assertion. G1–G18 unchanged; G19 severity promoted (R46 mod); G20 additive (R48); G21 additive (R51). The scorecard is regenerated (replaced, not appended) on each Step 5 iteration (R44).

**Scorecard assembly (in main context after lens merge, per iteration):**

1. **Dimension table** — Aggregate CRITICAL/WARNING/SUGGESTION/PASS counts by dimension across all lens findings that carry a severity tag. Three rows: Completeness, Correctness, Coherence. Counts sourced from lens output; when a lens did not emit per-check severity tags, treat its findings as unclassified (exclude from counts).

2. **Traceability matrix** — One row per requirement source:
   - When `openspecContext.requirements[]` is present (non-null): use each `{ reqId, name }` as a row. For each row, map to covering Task(s) by matching task descriptions that reference the requirement (by `reqId`, `name`, or delta-spec section title). Status: `covered` (≥1 task), `partial` (task exists but incomplete per lens finding), `uncovered` (no task maps to this requirement).
   - When `openspecContext.requirements` is null: build the matrix from the Step 1 requirements checklist instead. Note the downgrade in the scorecard header: `*(Matrix built from requirements checklist — requirement inventory unavailable)*`

3. **Verdict** — Derived from the aggregate of all findings across lens outputs AND Gate A caveats (when present), using the verbatim opsx:verify labels (R40):
   - Any finding with severity CRITICAL → verdict: *"…Fix before archiving."*
   - No CRITICAL, any WARNING → verdict: *"…Ready for archive (with noted improvements)."*
   - Only SUGGESTION or zero findings → verdict: *"All checks passed. Ready for archive."*

4. **Write the scorecard section** to the plan file. Placement: immediately after the last Task block, or after `## Suggested Review Dimensions` when that section exists, or after `## Guardrail Compliance` if no Suggested Review Dimensions. REGENERATE (replace) on each iteration — do not append a second copy.

**Review loop:**
- Approved → Step 6 is a no-op, proceed to Step 7
- Issues found → go to Step 6
- Max 3 iterations → use AskUserQuestion to surface unresolved issues to user. Context: the review loop ran 3 fix/re-review passes and still has open blocking issues — before asking, summarize what failed (the union of blocking findings across all lenses), scaled to `style.audience`, so the user isn't choosing blind. Offer **harden** (run `/harden` to analyze why this failed and propose stronger guardrails / dimensions / instructions that would catch it earlier next time — opt-in, no surface is edited without your approval; consequence: proposes preventive changes only, nothing is edited without a separate approval) alongside the existing escalation options. When the user selects **harden** (interactive mode only — suppressed when `--auto` is set), dispatch `Skill(harden)` with `--failure-text "Plan reviewer loop did not converge after 3 iterations. Outstanding issues: <union-of-blocking-issues-across-all-lenses>"`, `--skill plan`, `--step "Step 5 — review loop"`, `--operation "reviewer-loop max iterations"`. Implements R19.

## Step 6 (IMPROVE): Apply Review Fixes

Fix each blocking issue identified by the reviewer. Rewrite the plan file with fixes applied. If any revision changes the scope or approach from what was originally recorded, update the `## Deviations & assumptions` table accordingly.

**Gate B verdict wiring (implements R41 — Fixes #445):** The Gate B Verification Scorecard verdict is treated as an additional blocking-issue source using the same `Issues Found` path. This avoids divergent gate phrasing (`no-opposite-logical-vectors` guardrail) — the CRITICAL verdict does not have a separate code path; it injects findings into the same blocking-issue set that the `Issues Found` path already processes.

- When the Gate B verdict is CRITICAL: inject the scorecard CRITICAL findings into the blocking-issue list as if they were additional `Issues Found` findings. The plan enters Step 6 IMPROVE with these injected findings. The iteration counter (max 3) continues normally — Gate B CRITICAL does not create a new loop or counter.
- When the Gate B verdict is WARNING or SUGGESTION: no injection into Step 6. Caveats remain in the plan file. Proceed to Step 6.5 / Step 7 normally.
- When the Gate B verdict is PASS (clean): proceed normally.

**Material change detection (implements R64):**

Before rewriting the plan file with fixes, call `plan_support({action: "material_snapshot", filePath: planPath})` and store the returned `snapshot`. After the rewrite, call `plan_support({action: "material_compare", filePath: planPath, snapshot: <snapshot>})`. The tool compares all seven structural dimensions (task count, deviations rows, files-set, contracts, depends-on, key decisions, openspec-task mapping) and returns `material` (boolean) and `triggers` (list of fired trigger descriptions). If `material` is true, set `materialChangeDetected = true`. Wording-only and formatting-only fixes are NOT material (R64).

Re-dispatch the reviewer (back to Step 5 loop). When `materialChangeDetected` is true, the Step 5 merged dispatch path activates — see "Material change detection and merged re-dispatch" in Step 5.

If this is the 3rd iteration, use AskUserQuestion to surface remaining issues instead of looping.

## Step 6.5 (LINK VERIFICATION): Validate URLs in plan content (R18) — HARD GATE

After the reviewer loop converges (or the user resolves remaining issues), validate every URL embedded in the finalized plan file:

```
links_validate({ file: "$plan_path", offline: false })
→ { results: [{ url, line, status, reason, detail }] }
```

`expectedRepo` and `jiraSite` are auto-derived by the tool from the project's git remote and `~/.sdlc-cache/jira/` — the skill MUST NOT construct that context itself.

If any `results[]` entry has a non-`ok` `status`:
- Do NOT proceed to Step 7 (Handoff). The plan is not ready.
- Surface the violation list verbatim to the user.
- Stop. Do not retry. Do not edit URLs without user input. Do not bypass.

On all-clear, proceed to Step 7. Pass `offline: true` (replacing the old `SDLC_LINKS_OFFLINE=1` env var) to skip network reachability while keeping context-aware checks (GitHub identity match, Atlassian host match) — use in sandboxed CI.

## Step 6.6 (FORMAT VALIDATION): Validate plan structure — HARD GATE

After link verification passes, run the deterministic plan format validator. PF9 (Verification Scorecard presence) is applied only when this run executed Step 5 (the multi-lens review).

**Set `FINAL_FLAG` from the Step 0 routing branch THIS run took** — NOT from scorecard presence (deriving it from the artifact it checks would make PF9 circular):

- **Full-pipeline plan** (Step 0 routed through Step 5, the multi-lens review — i.e. ≥4 files): `FINAL_FLAG=true`. A scorecard is expected, so PF9 is enforced.
- **Lightweight plan** (Step 5 skipped): `FINAL_FLAG=false`. No scorecard expected, so PF9 is not applied.

**Set `TEMPLATE_PATH`:** When `FINAL_FLAG` is true (full-pipeline plan), set `TEMPLATE_PATH="$activeTemplatePath"` (the absolute path resolved in Step 0's template resolution). When `FINAL_FLAG` is false (lightweight plan), omit `template` entirely — PF10 is skipped along with PF9.

Call:

```
validate({ action: "plan_format", file: "$plan_path", final: <FINAL_FLAG>, template: "$TEMPLATE_PATH" })
→ { findings: [{ id, severity, message, path }] }
```

`file` is required on every call (the tool's own doc comment names it as the plan_format target) — do not drop it chasing a shorthand example that omits it. Omit `template` when `TEMPLATE_PATH` is unset rather than passing an empty string.

If `findings` is non-empty:
- Do NOT proceed to Step 7 (Handoff). The plan is not ready.
- Surface every finding (`id`, `severity`, `message`, `path`) verbatim to the user.
- Stop. Do not retry. Do not auto-edit. Do not bypass.

If `findings` is empty, the plan passed every applicable PF check — proceed to Step 7.

## Step 7: Handoff

**Gate B scorecard pointer (implements R41 — Fixes #445):** Before the plan-mode or normal-mode branch below, when a `## Verification Scorecard` section exists in the plan file (i.e., Gate B ran during Step 5), surface a one-line verdict reference above the `ship` / `execute` / `done` menu:

> Verification Scorecard: `<verdict line>` — see `## Verification Scorecard` in the plan for details.

Where `<verdict line>` is the verbatim verdict label from the scorecard: *"All checks passed. Ready for archive."*, *"…Ready for archive (with noted improvements)."*, or *"…Fix before archiving."*. When no scorecard is present (non-OpenSpec plan or scorecard was not generated), omit this line entirely.

**Plan mode:** Announce the plan path and propose execution. Prepend any advisory output from the wrapper above the `ship` / `execute` lines:

> Plan written to `<path>`. On approval:
>   ship    — run the full pipeline: execute → commit → review → version → PR (/ship)
>   execute — execute the plan only (/execute)

Then call ExitPlanMode. Do NOT invoke execute or ship in this turn — they run after the user accepts in the next turn.

**Normal mode:** Announce the plan path, then present the Workflow Continuation menu (see below). Prepend any advisory output from the wrapper above the menu's `ship` / `execute` / `done` lines.

Do NOT report the plan as "validated" on format-floor PASS alone. Format floor = deterministic structure. Render/narrate quality was judged by the content-coverage lane in Step 3. Surface both, separately.

## Error Recovery

| Error | Recovery |
|---|---|
| Spec/requirements not found | Ask user to provide path or paste content |
| Codebase exploration fails (too large) | Ask user to point to relevant directories |
| Plan reviewer loop exceeds 3 iterations | Surface to user for guidance |
| Requirements are contradictory | Flag specific contradictions, ask user to resolve |
| User approves but output path fails | Retry with a different path; offer to print plan to screen |

## DO NOT

- Write implementation code in the plan (code snippets for patterns are fine; full implementations are not)
- Mandate TDD for every task — match verification to task type
- Invoke execute within the same turn as plan (execution happens in the next turn after user acceptance)
- Create plans with fewer than 2 tasks (just do the work directly)
- Skip the plan review loop (unless lightweight routing applies)
- Use absolute file paths that only work on one machine
- Put plans in `$TMPDIR` — plans should survive session boundaries
- Put plans in plugin-branded directories (no `docs/superpowers/plans/`)
- Ignore plan mode's designated file path when plan mode is active — always write to it
- Use TodoWrite for lightweight plans — it adds overhead without value
- Prompt the user at Step 0 session-recovery or Step 4 — those steps are autonomous (single-touchpoint at Step 7, implements R22/R23)
- Treat G19 findings as advisory — G19 is error-severity and blocks plan approval

## Gotchas

**Vague task descriptions produce hallucinated implementations.** "Add authentication" is not a task. "Add JWT token validation middleware at `src/middleware/auth.ts` that checks the Authorization header and attaches the decoded user to `req.user`" is a task. If you can't describe the exact file and behavior, the task isn't ready.

**Complexity classification drift.** A task titled "add a config key" may be Trivial in the title but Standard in practice if it requires a new schema, a migration, and downstream changes. Classify by the full description, not the title.

**Implicit dependencies.** Two tasks that don't share a file may still have dependencies — barrel files, type re-exports, config registrations, route ordering. Check for these during Step 3 critique.

**Over-decomposition.** If most tasks are Trivial, the plan is over-decomposed. Each task should represent a meaningful unit of work — not a single line change.

**Under-decomposition.** A task that creates 8 files or implements 3 independent behaviors will fail in execution. If a task touches > 5 files, split it.

**Plan-execution format mismatch.** The plan MUST include Complexity, Risk, Depends on, and Verify fields per task — execute consumes these for wave building. Missing metadata forces inference, which is slower and less accurate.

**Plan file is the single source of truth.** All working state lives in the plan file. Do not create temporary files, scratchpads, or side documents. Exploration findings belong in the `## Research Findings` section (template-required, persists in the final plan). The `## Requirements` section is temporary scaffolding removed in Step 2 post-write cleanup.

## Learning Capture

After writing the plan, append to `.sdlc-v2/learnings/log.md`:

- Requirements that needed significant clarification before decomposition
- Scope decisions (what was included/excluded and why)
- Codebase patterns that influenced task structure
- Plans that were over/under-decomposed on first draft

Format:
```
## YYYY-MM-DD — plan: <feature name>
<what was learned>
```

## Workflow Continuation

After writing the plan (normal mode only), present the user with available next actions:

```
What would you like to do next?
  ship     — execute, commit, review, version, and PR (/ship)
  execute  — execute the plan only (/execute)
  done     — stop here

Select:
```

On selection, invoke the chosen skill using the Skill tool. On "done", end without further action.

## See Also

- `./plan-template-default.md` — shipped default plan template (section list, discovery questions, verification patterns)
- `./plan-reviewer-prompt.md` — plan review subagent template
- `./plan-format-reference.md` — plan document format specification
- [`/execute`](../execute/SKILL.md) — skill that executes the plans this skill produces
- [`docs/plan-architecture.md`](../../../../docs/plan-architecture.md) — plan skill architecture reference
