---
name: harden
description: "Use this skill after an SDLC pipeline failure to analyze hardening surfaces (plan and execute guardrails, review dimensions, copilot instructions) and propose user-approved edits that would prevent the same class of failure next time. Alternatively, use --from-learnings to batch-triage all non-harden learnings entries through the orchestrator. Strengthen-only in v1 — never relaxes or removes existing rules. Required arguments: --failure-text <string> --skill <caller-name> (or --from-issue <num> --skill <name>, or --from-learnings alone). Optional: --step, --operation, --exit-code, --error-type, --user-intent, --args-string. Triggers on: harden, strengthen guardrails, prevent this failure, learn from this failure, after pipeline failure, triage learnings."
user-invocable: true
argument-hint: "--failure-text <text> --skill <name> [--step <s>] [--operation <op>] | --from-learnings"
model: sonnet
---

# Hardening After a Pipeline Failure

This skill runs after an SDLC pipeline failure to propose user-approved edits to
the project's hardening surfaces (plan guardrails, execute guardrails, review
dimensions, copilot instructions) so the same class of failure is caught earlier
next time. Strengthen-only: never relaxes or removes an existing rule.

**Announce at start:** "I'm using harden (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

## Port Notes (read before using this skill)

This is a Go/MCP port of the original script-driven skill. Where its tool surface
differs from the source procedure, this port adapts as follows — flagged here
rather than left implicit:

- **Validate-before-write becomes write-then-validate-and-revert.** The
  `validate` MCP tool (`action: "guardrails"` / `"dimensions"`) checks whatever
  is currently on disk — it has no "validate this in-memory prospective JSON"
  mode. Step 1's pre-flight (inside `prepare_orchestrator`, mode `"harden"`) already guarantees the
  on-disk guardrails/dimensions are valid *before* this skill starts, so any
  `validate` finding observed immediately after a proposal's write is
  attributable to that write. Step 5a/5b apply the edit first, validate second,
  and revert (re-write the prior content) on failure — see Step 5a for detail.
- **No `manifest.errorReportSkillPath` dependency.** This port dispatches
  `error-report` the same way every other ported skill does — "invoke
  error-report, provide: Skill/Step/Operation/Error/Suggested
  investigation" — rather than resolving and following a `REFERENCE.md` path.
  `prepare_orchestrator`'s (mode `"harden"`) manifest still carries an `errorReportSkillPath` field (and
  will typically log a load error for it, since `error-report/REFERENCE.md`
  is not shipped in this port); this skill does not read either.
- Copilot-mirror generation uses the `dimensions_render_instructions` MCP tool.
  Pass `projectRoot: <CONTENT_ROOT>` explicitly (see Step 5b) so the mirror is
  written under the active worktree, not the main one.

## Step 0 — Parse Arguments (R1, R2, R19)

**Mutually exclusive primary inputs:**

| Mode | Flag | Required when |
|---|---|---|
| Inline failure text | `--failure-text <string>` | Default mode, unless another is used |
| GitHub issue fetch | `--from-issue <num>` | Alternative to `--failure-text` |
| Learnings triage | `--from-learnings` | Alternative to `--failure-text` / `--from-issue` |

If more than one of `--failure-text`, `--from-issue`, or `--from-learnings` is
provided, stop immediately with a clear mutual-exclusion error message. Do not
call `prepare_orchestrator`.

If none of `--failure-text`, `--from-issue`, or `--from-learnings` is present,
stop with an error message.

Required flag: `--skill` — required for `--failure-text` and `--from-issue`
modes; **not required** (and ignored if passed) for `--from-learnings` (the
skill name is parsed from each entry's header). Optional: `--step`,
`--operation`, `--exit-code`, `--error-type`, `--user-intent`,
`--args-string`.

**When `--from-issue <num>` is used:** `prepare_orchestrator` (mode `"harden"`) fetches the GitHub issue
body automatically (via `gh issue view`). When the issue carries the
`mcp-failure` label, the tool pre-sets `classification_hint: "plugin-defect"`
in the manifest. In that case, skip Step 3 — proceed directly to Step 4, which
will route to Step 6 (PLUGIN-DEFECT ROUTE) without dispatching the orchestrator.
Pass `fromIssue: "<num>"` to the Step 1 tool call.

## Step 1 — CONSUME: Call `prepare_orchestrator` (mode: `"harden"`) (R4, R13)

```
prepare_orchestrator({
  mode: "harden",
  failureText: "<failure text, empty if --from-issue is used>",
  fromIssue: "<issue number, empty if --failure-text is used>",
  skill: "<calling/failing skill name>",
  step: "<step name, if any>",
  operation: "<operation, if any>",
  exitCode: "<exit code or HTTP status, if any>",
  errorType: "<error type, if any>",
  userIntent: "<user intent, if any>",
  argsString: "<invocation arguments, if any>",
  skipConfigCheck: false,
}) → { manifestPath }
```

Empty values for optional fields are tolerated.

**On tool error:** show the error message to the user and stop. Do **not**
recursively dispatch this skill on its own crash — a `prepare_orchestrator` crash (as
opposed to a validation error) is a plugin defect and belongs in
`error-report`, not another harden run. A validation error (missing
required field, `--failure-text`/`--from-issue` mutual exclusion, or R16
pre-flight failure on the *existing* guardrails/dimensions files) is a stop
condition, not a crash — do not offer `error-report` for those.

The manifest now includes a `history` section (when `.sdlc-v2/history/` exists)
with `recentRuns` (last 10 pipeline run records from `runs.jsonl`) and
`openDeferred` (unresolved deferred issues from `deferred.json`). The
orchestrator uses this as additional evidence — e.g. if the same guardrail hit
appears in 3+ recent runs, proposal severity should escalate.

**Do NOT read the full manifest file contents into the main context yet.**
Step 2 needs only the classification preview (a small subset), and Step 3 hands
the full manifest path to the orchestrator agent.

There is no bash trap spanning this run — `manifestPath` is a plain return
value from the tool. Clean it up explicitly with `rm -f "<manifestPath>"` at
every stop point below.

### Step 1 — Alternative: `--from-learnings` Mode

When `--from-learnings` is set, skip the normal `prepare_orchestrator` call
above. Instead, triage every non-harden learnings entry through the
orchestrator individually. Steps 2–4 are subsumed into this alternative path;
resume at Step 5 with the grouped results.

#### 1-FL.1 — Read and Parse Entries

```
learnings_log({action: "read"}) → {ok, exists, content}
```

If `exists == false` or `content` is empty, report `No learnings to triage.`
and exit cleanly.

Parse entries **exactly as `learningsRemove` does** — split the full content on
`\n\n` (double newline), treat block[0] as the header (not a removable entry),
and number the remaining blocks 1-indexed. Preserve each entry's original index
throughout — never renumber after filtering.

#### 1-FL.2 — Filter: Skip Harden-Prefixed Entries

An entry is harden-prefixed when its first line matches the pattern
`## YYYY-MM-DD — harden:` (em-dash `—`, not a plain hyphen). These entries are
the skill's own learning-capture output (Step 7) and the plan skill depends on
their `Dimensions:` line for duplicate-dimension suppression. Skip them — do
not dispatch, do not remove.

If no candidate entries remain after filtering, report `All learnings entries
are harden-owned — nothing to triage.` and exit cleanly.

#### 1-FL.3 — Preview Candidates

Display a compact summary — one line per candidate:

```
harden --from-learnings: {N} candidate entries (of {total} total, {skipped} harden-owned skipped)

  [{index}] {parsed_skill}: {first 80 chars of entry}
  [{index}] {parsed_skill}: {first 80 chars of entry}
  ...
```

Parse the skill name from each entry's header line: pattern
`## YYYY-MM-DD — <skill>: ...` extracts `<skill>`. When the header does not
match this pattern, use `"unknown"` as the skill name.

#### 1-FL.4 — Per-Entry Orchestrator Dispatch

For each candidate entry, in order:

1. Call `prepare_orchestrator`:
   ```
   prepare_orchestrator({
     mode: "harden",
     failureText: "<full entry text>",
     skill: "<parsed_skill>",
     skipConfigCheck: true,
   }) → { manifestPath }
   ```
   `skipConfigCheck: true` — config validation does not need to re-run per
   entry. Store the `manifestPath` in a side table alongside the entry's
   original 1-indexed position.

   **On tool error for a single entry:** log the error, record the entry as
   errored in the side table, and continue to the next. Do not abort the entire
   triage run for one entry's prepare failure.

2. Read `repository.contentRoot` from the manifest. Dispatch the
   harden-orchestrator agent exactly as in Step 3:
   ```
   Agent({
     subagent_type: "sdlc:harden-orchestrator",
     model: "haiku",
     prompt: "MANIFEST_FILE: <manifestPath>\nPROJECT_ROOT: <contentRoot>",
   }) → RESULT
   ```

3. Record `{entryIndex, parsedSkill, manifestPath, RESULT}` in the side table.
   If JSON parse of the orchestrator response fails, record the entry as
   errored (no proposals) and continue.

#### 1-FL.5 — Group by Classification

After all entries are dispatched, group the side table by
`RESULT.classification`:

- **user-code** — entries whose proposals go through Step 5 (PRESENT and APPLY)
- **ambiguous** — same as user-code for proposals; additionally eligible for
  Step 5c (ambiguous upstream-report offer) per entry
- **plugin-defect** — entries routed to Step 6 (PLUGIN-DEFECT ROUTE)

Present the grouped summary:

```
harden --from-learnings: classification results

  user-code:      {count} entries, {proposal_count} proposals
  ambiguous:      {count} entries, {proposal_count} proposals
  plugin-defect:  {count} entries
  errored:        {count} entries (skipped)
```

Then proceed through Steps 5–6 with the grouped results. Process user-code
entries first, then ambiguous, then plugin-defect. Within each classification
group, proposals are presented per-entry in the Step 5 loop, following the same
R-iteration-write contract, apply/skip/cancel flow, and 5a/5b/5c rules as the
normal path. For plugin-defect entries, follow Step 6 per entry.

Track which entries had at least one proposal receive an `apply` answer — these
are **addressed** entries.

#### 1-FL.6 — Remove Addressed Entries

After all proposals have been presented and the apply/skip/cancel loop
completes, determine which entries are **addressed**: an entry is addressed if
and only if at least one proposal derived from it received an `apply` answer.
Zero-proposal entries, all-skipped entries, and plugin-defect entries (even if
error-report was dispatched) are **not** removed.

Issue a **single** `learnings_log` remove call with all addressed indices:

```
learnings_log({action: "remove", indices: [<addressed entry indices>]})
```

Do **not** issue sequential single-index remove calls — each remove rewrites
the file and shifts entry positions, so sequential calls would delete the wrong
entries. If no entries were addressed, skip the remove call.

#### 1-FL.7 — Cleanup

`rm -f` every `manifestPath` in the side table on every exit path — including
cancel mid-loop, zero-candidate exit, and normal completion. Then proceed to
Step 7 (Learning Capture) as usual.

## Step 2 — CLASSIFY: Surface the Failure Classification (R5, R9)

> **`--from-learnings` mode:** Steps 2–4 are handled within the Step 1
> alternative path (1-FL.4 and 1-FL.5). Skip directly to Step 5.

Read **only** the `failure.*` and `classification_hint` fields from the file at
`manifestPath` — do not load the full surface arrays into the main context.
Display a short preview to the user:

```
harden: failure context loaded
  Skill:        {failure.skill}
  Step:         {failure.step or "—"}
  Operation:    {failure.operation or "—"}
  Failure (first 200 chars): {failure.text[:200]}
  Classification hint:  {classification_hint or "(none — orchestrator will classify)"}
```

When `classification_hint == "plugin-defect"` (set by `prepare_orchestrator` when
`fromIssue` fetches an issue with the `mcp-failure` label), skip Step 3 —
proceed directly to Step 4, which will route to Step 6 (PLUGIN-DEFECT ROUTE)
without dispatching the orchestrator. The manifest already carries the pre-set
classification; the orchestrator agent is not needed.

The orchestrator (Step 3) is responsible for the authoritative classification
in all other cases. Continue to Step 3.

## Step 3 — ANALYZE: Dispatch the harden-orchestrator Agent (R6)

Read `repository.contentRoot` and `repository.root` from the manifest JSON at
`manifestPath` (a plain Read + JSON parse — no shell one-liner needed):

- `CONTENT_ROOT` = `repository.contentRoot` (active worktree — dimensions/copilot paths rooted here).
- `MAIN_ROOT` = `repository.root` (main worktree — `.sdlc-v2/config.json` rooted here).

Do NOT recompute either via `git`. Store both; they are needed in Step 5.

The manifest's `pipeline.issues` (present when the latest ship/execute state
carries any, omitted otherwise) is structured, pre-parsed failure context —
wave/task/step, severity, category, summary — for the orchestrator to read
alongside `pipeline.shipState`/`pipeline.executeState`. Do not load it into
the main context here; like the surface arrays, it is for the orchestrator
below to read directly from `manifestPath`.

Use the `Agent` tool with:

- `subagent_type`: `sdlc:harden-orchestrator`
- `model`: `haiku`
- `prompt` (exactly two lines, no other content):

  ```text
  MANIFEST_FILE: <manifestPath>
  PROJECT_ROOT: <CONTENT_ROOT>
  ```

The orchestrator returns ONLY a JSON object:

```json
{
  "classification": "user-code | plugin-defect | ambiguous",
  "classificationRationale": "string",
  "routeToErrorReport": false,
  "errorReportPayload": null,
  "proposals": [ ... ]
}
```

Capture the returned object as `RESULT`. If JSON parse fails, stop and surface
the raw response to the user (no retry — the failure is unrelated to the
failure being analyzed). `rm -f "<manifestPath>"` before stopping.

## Step 4 — Branch on Classification (R9)

If `RESULT.classification == "plugin-defect"` AND `RESULT.routeToErrorReport ==
true`: jump to **Step 6 — PLUGIN-DEFECT ROUTE**. Skip Step 5 (PRESENT and APPLY)
entirely — no surface edits are appropriate for plugin defects.

Otherwise (`user-code` or `ambiguous`), display the classification and rationale
to the user, then continue to Step 5 (PRESENT and APPLY).

```
Classification: {RESULT.classification}
Rationale:      {RESULT.classificationRationale}
```

If `RESULT.proposals` is empty, report `No actionable hardening proposals — the
failure signal does not point at any of the loaded surfaces.`, `rm -f
"<manifestPath>"`, and exit cleanly.

## Step 5 — PRESENT and APPLY (R7, R8, R10, R12, C9, C10, R-iteration-write)

**Per-iteration contract (R-iteration-write) — applies to every pass through the proposal loop:**
1. **Re-read before acting:** At the start of each iteration, re-read `targetFile` from disk. Never rely on an in-memory copy from a previous write.
2. **Write before advancing:** Persist the approved change to disk (via Edit/Write) before presenting the next proposal. Do not accumulate approved changes across proposals and write them together.
3. **No cross-proposal accumulation:** Hold only the current proposal's patch in memory. Clear per-proposal state after each write.
4. **Halt on failure:** If validation or the write itself fails for a proposal, do not silently advance to the next proposal — halt iteration for this proposal and surface the error per 5a.

For each proposal in `RESULT.proposals`, present the full patch preview to the
user. Then use `AskUserQuestion`:

> Proposal {i+1} of {N}: {action} on {surface}
> Target: {targetFile}
> Rationale: {rationale}
>
> Preview:
> ```
> {patch}
> ```
>
> Apply this proposal?

Options: **apply** | **skip** | **cancel**

- **apply** — proceed to write and validate
- **skip** — record the proposal as skipped, continue to the next
- **cancel** — abort the entire skill (no further proposals processed);
  `rm -f "<manifestPath>"`

### 5a. Write, Then Validate, Then Revert on Failure (R12, R-iteration-write)

**Re-read `targetFile` from disk now** (R-iteration-write rule 1) — do not use
any in-memory state from a prior iteration. Keep the pre-write content in
memory only long enough to revert if validation fails (cleared once this
proposal's iteration completes).

When the user selects **apply**:

1. Apply the change to `targetFile` with Edit (preferred) or Write.
2. Validate immediately:
   - For `surface == "plan-guardrails"` or `"execute-guardrails"`: `targetFile`
     is `<MAIN_ROOT>/.sdlc-v2/config.json` (already an absolute path rooted at
     `repository.root` in the proposal — guardrail config is shared/main-rooted
     even when this skill runs from a linked worktree). Call
     `validate({ action: "guardrails", section: "plan" | "execute" })`
     (section matches which surface this proposal targets).
   - For `surface == "review-dimensions"`: call
     `validate({ action: "dimensions" })`.
   - For `surface == "copilot-instructions"`: no schema — skip validation,
     continue to 5b.
3. **If `findings` is non-empty:** the just-applied write introduced a problem
   (Step 1's pre-flight already guaranteed the pre-existing on-disk state was
   clean, so any finding now is caused by this proposal). Revert `targetFile`
   to the content re-read at the top of this step, surface the findings to the
   user, and use `AskUserQuestion` to offer **retry** (let the user adjust the
   patch inline, then repeat from step 1) or **cancel** (skip this proposal).
   Never leave a schema-invalid edit in place.
4. **If `findings` is empty** (or validation was skipped for
   `copilot-instructions`): continue to 5b.

### 5b. Copilot Mirror for New Review Dimensions (R-copilot-mirror)

Display a one-line confirmation for 5a's successful write:

```
Applied {action} on {surface} → {targetFile}
```

Immediately after (still this iteration, before advancing to the next
proposal), gate on BOTH: (a) `proposal.surface === "review-dimensions"` AND
`proposal.targetFile` (an absolute path rooted at `repository.contentRoot`)
contains `.sdlc-v2/review-dimensions/`, AND (b) `proposal.action === "add"` (a NEW
dimension — existing dimensions are NOT retroactively mirrored per R8/C9;
`strengthen`/`consolidate` actions on an already-mirrored dimension do not
re-run this). When the gate does not hold, skip this block entirely.

When it holds:

1. **Generate and write the mirror deterministically** (NEVER hand-author it —
   scripts-over-LLM-logic):

   ```
   dimensions_render_instructions({
     file: "<proposal.targetFile>",
     commonFile: "<CONTENT_ROOT>/.sdlc-v2/review-dimensions/_common.md",
     projectRoot: "<CONTENT_ROOT>",
   }) → { ok, path }
   ```

   Pass `commonFile` unconditionally — the tool silently omits the "Common
   Review Instructions" section when that file does not exist, so no
   existence check is needed first. `projectRoot` is required here (defaults
   to the main worktree otherwise) so the mirror lands under the active
   worktree, matching source's #474 requirement.

2. **On tool error:** do NOT silently advance to the next proposal (halt per
   R-iteration-write rule 4). Surface the partial state explicitly:
   `Dimension written to <proposal.targetFile> but the Copilot mirror could not
   be created (<error>) — resolve manually before continuing.`

3. **On success**, display: `Mirrored review dimension → {path}`.

**Existing mirror already present:** the orchestrator only proposes an `add`
for a not-yet-created dimension, so this branch is reached only if a mirror was
manually pre-seeded. `dimensions_render_instructions` overwrites the mirror
file wholesale — that is fine for a fresh `add` (there is no prior *approved*
content to preserve), but if the pre-seeded file had hand-added rows you want
to keep, diff before applying and fold them back in manually; this port has no
merge-only mode for this tool.

Severity vocabulary per surface is canonical in `internal/dimensions`
(`ValidSeverities`, guardrail severities); see spec R10 + R17. The orchestrator
already chose the correct vocabulary in its proposal — never substitute one for
the other.

**When `proposal.action === "consolidate"` (R15):** the proposal targets an
existing guardrail by id. Read the current `.sdlc-v2/config.json` from disk,
locate the guardrail in `<section>.guardrails[]` by the id specified in the
proposal's `patch`, and replace its fields with the proposal's merged values
(description, severity). Do NOT remove fields; do NOT lower severity
(strengthen-only invariant — R8/C9). If no guardrail with the target id exists
in the current file, treat the proposal as malformed and surface to the user.
`consolidate` goes through the same write-then-validate-then-revert flow as 5a.

### 5c. Ambiguous upstream-report offer (R-ambig-offer)

When `RESULT.classification === "ambiguous"` AND
`RESULT.errorReportPayload != null`, the orchestrator concluded the failure
*may* be a plugin defect even though the evidence was not strong enough to
classify it as one. After the per-proposal apply/skip flow above completes,
present an opt-in upstream-report offer:

> This failure may also be a plugin defect. File a GitHub issue?

Use AskUserQuestion with options: **invoke error-report** | **skip**.

- On `invoke error-report`: invoke `error-report`, providing the
  fields from `RESULT.errorReportPayload` (Skill/Step/Operation/Error/Suggested
  investigation — same shape and idiom used everywhere else in this plugin).
- On `skip`: record the skip in Step 7 Learning Capture and exit cleanly.

The strengthen-only invariant is preserved — no surface is auto-edited; the
user explicitly approves the dispatch. When `RESULT.errorReportPayload == null`
on `ambiguous` (pure user-code ambiguity), this sub-step is suppressed entirely
— do not surface the prompt.

## Step 6 — PLUGIN-DEFECT ROUTE: Dispatch error-report (R9)

When `RESULT.classification == "plugin-defect"`:

1. Display `RESULT.errorReportPayload` to the user as the proposed
   `error-report` dispatch payload.
2. Use AskUserQuestion: **invoke error-report** | **cancel**.
3. On `invoke error-report`: invoke `error-report`, providing:
   `skill=<failure.skill>`, `step=<failure.step>`, `operation=<failure.operation>`,
   `error=<failure.text>`, `exitOrHttpCode=<failure.exitCode>`,
   `errorType=<failure.errorType or "script crash">`.
4. Do NOT edit any user-side hardening surface in the plugin-defect path. The
   no-silent-write invariant applies here too — the user must explicitly
   approve the error-report dispatch.

`rm -f "<manifestPath>"` on every exit path from this step.

## Step 7 — Learning Capture

Call `learnings_log` to append an entry summarizing the hardening action:

```
learnings_log({action: "append", entry: "## YYYY-MM-DD — harden: <classification> for <failure.skill> at <failure.step>\nApplied: <count> proposal(s) across <surface-list> | Skipped: <count> | Routed: <yes|no>\nAmbiguousOffer: <not-applicable|offered-dispatched|offered-skipped>\nTrigger: <first 80 chars of failure.text>\nDimensions: <comma-separated dimension names that were created or modified>"})
```

The `Dimensions:` line MUST be included **only when `<surface-list>` includes
`review-dimensions`** (i.e., at least one review-dimension file was created or
modified during this hardening run). When `review-dimensions` is NOT in the
surface-list, the `Dimensions:` line MUST be omitted entirely — do not emit it
with an empty value.

This line exists so that plan's dimension-coverage gate can
deterministically suppress duplicate dimension proposals on subsequent runs
within the same PR commit window, by reading the last 100 lines via
`learnings_log({action: "read", tailLines: 100})` for recent `harden` entries
whose `Dimensions:` line names the candidate dimension.

The `AmbiguousOffer` line records the Step 5c outcome:

- `not-applicable` — classification was not `ambiguous`, OR was `ambiguous` with
  `errorReportPayload == null` (no plugin evidence; offer suppressed).
- `offered-dispatched` — Step 5c offered the upstream-report and the user chose
  `invoke error-report`.
- `offered-skipped` — Step 5c offered the upstream-report and the user chose
  `skip`.

Mirror the append pattern used by `commit` and `execute`. Create
the `.sdlc-v2/learnings/` directory and `log.md` file if they don't exist.

`rm -f "<manifestPath>"` here if it has not already been removed by an earlier
step (it should have been — this is a defensive final check, not a new
cleanup path).

## DO NOT

- Edit any surface without an `apply` AskUserQuestion answer recorded for that
  specific proposal — the no-silent-write invariant is non-negotiable.
- Accumulate approved changes across multiple proposals and write them together
  — each approved proposal MUST be written to disk immediately before advancing
  to the next proposal (R-iteration-write).
- Propose relaxing or removing existing rules — v1 is strengthen-only.
- Run full-suite or wide-subset eval automatically — a single targeted check
  scoped to the change is allowed; tight-loop retries are not.
- Invoke `error-report` for `user-code` classifications — only the
  `plugin-defect` branch (Step 6) and the `ambiguous`-with-payload branch (5c)
  route there.
- Read the full manifest contents into the main context — Step 2 reads only
  `failure.*` and `classification_hint`; the orchestrator owns the rest.
- Auto-dispatch this skill from a caller skill without explicit user selection
  in the caller's failure-handling menu.
- Recursively dispatch this skill on its own `prepare_orchestrator` or orchestrator
  crash — log the failure and stop.
- Override severity vocabulary chosen by the orchestrator (R10/R17) — each
  surface has its own canonical vocabulary; never substitute one for the other.
- Leave a schema-invalid edit in place after a failed post-write `validate`
  call — revert per 5a.
- Issue sequential single-index `learnings_log` remove calls in
  `--from-learnings` mode — each remove rewrites the file and shifts entry
  positions. Always collect all addressed indices and issue one batch remove
  call (1-FL.6).

## When This Skill Is Invoked

- **Standalone:** `/harden --failure-text "..." --skill plan --step "Step 5" --operation "reviewer-loop"`
- **Learnings triage:** `/harden --from-learnings`
- **Caller-dispatched:** Caller-dispatched skills present an opt-in menu option at their failure surfaces that dispatches `Skill(harden)` with the same flag shape. `ship` is intentionally NOT a caller — it delegates failure handling to its sub-skills, so harden reaches the user through whichever sub-skill failed.

## See Also

- [`/error-report`](../error-report/SKILL.md) — plugin-defect route
- [`/setup`](../setup/SKILL.md) — initial guardrail/dimension authoring
