---
name: harden-sdlc
description: "Use this skill after an SDLC pipeline failure to analyze hardening surfaces (plan and execute guardrails, review dimensions, copilot instructions) and propose user-approved edits that would prevent the same class of failure next time. Strengthen-only in v1 — never relaxes or removes existing rules. Required arguments: --failure-text <string> --skill <caller-name>. Optional: --step, --operation, --exit-code, --error-type, --user-intent, --args-string. Triggers on: harden, strengthen guardrails, prevent this failure, learn from this failure, after pipeline failure."
user-invocable: true
argument-hint: "--failure-text <text> --skill <name> [--step <s>] [--operation <op>]"
model: sonnet
---

# Hardening After a Pipeline Failure

This skill runs after an SDLC pipeline failure to propose user-approved edits to
the project's hardening surfaces (plan guardrails, execute guardrails, review
dimensions, copilot instructions) so the same class of failure is caught earlier
next time. Strengthen-only: never relaxes or removes an existing rule.

**Announce at start:** "I'm using harden-sdlc (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

## Port Notes (read before using this skill)

This is a Go/MCP port of the original script-driven skill. Where its tool surface
differs from the source procedure, this port adapts as follows — flagged here
rather than left implicit:

- **Validate-before-write becomes write-then-validate-and-revert.** The
  `validate` MCP tool (`action: "guardrails"` / `"dimensions"`) checks whatever
  is currently on disk — it has no "validate this in-memory prospective JSON"
  mode. Step 1's pre-flight (inside `harden_prepare`) already guarantees the
  on-disk guardrails/dimensions are valid *before* this skill starts, so any
  `validate` finding observed immediately after a proposal's write is
  attributable to that write. Step 5a/5b apply the edit first, validate second,
  and revert (re-write the prior content) on failure — see Step 5a for detail.
- **No `manifest.errorReportSkillPath` dependency.** This port dispatches
  `error-report-sdlc` the same way every other ported skill does — "invoke
  error-report-sdlc, provide: Skill/Step/Operation/Error/Suggested
  investigation" — rather than resolving and following a `REFERENCE.md` path.
  `harden_prepare`'s manifest still carries an `errorReportSkillPath` field (and
  will typically log a load error for it, since `error-report-sdlc/REFERENCE.md`
  is not shipped in this port); this skill does not read either.
- Copilot-mirror generation uses the `dimensions_render_instructions` MCP tool.
  Pass `projectRoot: <CONTENT_ROOT>` explicitly (see Step 5b) so the mirror is
  written under the active worktree, not the main one.

## Step 0 — Parse Arguments (R1, R2, R19)

**Mutually exclusive primary inputs:**

| Mode | Flag | Required when |
|---|---|---|
| Inline failure text | `--failure-text <string>` | Always, unless `--from-issue` is used |
| GitHub issue fetch | `--from-issue <num>` | Alternative to `--failure-text` |

If both `--failure-text` and `--from-issue` are provided simultaneously, stop
immediately with a clear mutual-exclusion error message. Do not call
`harden_prepare`.

If neither `--failure-text` nor `--from-issue` is present, stop with an error
message.

Required flag (always): `--skill`. Optional: `--step`, `--operation`,
`--exit-code`, `--error-type`, `--user-intent`, `--args-string`.

**When `--from-issue <num>` is used:** `harden_prepare` fetches the GitHub issue
body automatically (via `gh issue view`). When the issue carries the
`mcp-failure` label, the tool pre-sets `classification_hint: "plugin-defect"`
in the manifest. In that case, skip Step 3 — proceed directly to Step 4, which
will route to Step 6 (PLUGIN-DEFECT ROUTE) without dispatching the orchestrator.
Pass `fromIssue: "<num>"` to the Step 1 tool call.

## Step 1 — CONSUME: Call `harden_prepare` (R4, R13)

```
harden_prepare({
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
recursively dispatch this skill on its own crash — a `harden_prepare` crash (as
opposed to a validation error) is a plugin defect and belongs in
`error-report-sdlc`, not another harden-sdlc run. A validation error (missing
required field, `--failure-text`/`--from-issue` mutual exclusion, or R16
pre-flight failure on the *existing* guardrails/dimensions files) is a stop
condition, not a crash — do not offer `error-report-sdlc` for those.

**Do NOT read the full manifest file contents into the main context yet.**
Step 2 needs only the classification preview (a small subset), and Step 3 hands
the full manifest path to the orchestrator agent.

There is no bash trap spanning this run — `manifestPath` is a plain return
value from the tool. Clean it up explicitly with `rm -f "<manifestPath>"` at
every stop point below.

## Step 2 — CLASSIFY: Surface the Failure Classification (R5, R9)

Read **only** the `failure.*` and `classification_hint` fields from the file at
`manifestPath` — do not load the full surface arrays into the main context.
Display a short preview to the user:

```
harden-sdlc: failure context loaded
  Skill:        {failure.skill}
  Step:         {failure.step or "—"}
  Operation:    {failure.operation or "—"}
  Failure (first 200 chars): {failure.text[:200]}
  Classification hint:  {classification_hint or "(none — orchestrator will classify)"}
```

When `classification_hint == "plugin-defect"` (set by `harden_prepare` when
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
- `MAIN_ROOT` = `repository.root` (main worktree — `.sdlc/config.json` rooted here).

Do NOT recompute either via `git`. Store both; they are needed in Step 5.

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

**Per-iteration contract (R-iteration-write, issue #387) — applies to every pass through the proposal loop:**
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
     is `<MAIN_ROOT>/.sdlc/config.json` (already an absolute path rooted at
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

### 5b. Copilot Mirror for New Review Dimensions (R-copilot-mirror, issue #456)

Display a one-line confirmation for 5a's successful write:

```
Applied {action} on {surface} → {targetFile}
```

Immediately after (still this iteration, before advancing to the next
proposal), gate on BOTH: (a) `proposal.surface === "review-dimensions"` AND
`proposal.targetFile` (an absolute path rooted at `repository.contentRoot`)
contains `.sdlc/review-dimensions/`, AND (b) `proposal.action === "add"` (a NEW
dimension — existing dimensions are NOT retroactively mirrored per R8/C9;
`strengthen`/`consolidate` actions on an already-mirrored dimension do not
re-run this). When the gate does not hold, skip this block entirely.

When it holds:

1. **Generate and write the mirror deterministically** (NEVER hand-author it —
   scripts-over-LLM-logic):

   ```
   dimensions_render_instructions({
     file: "<proposal.targetFile>",
     commonFile: "<CONTENT_ROOT>/.sdlc/review-dimensions/_common.md",
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
existing guardrail by id. Read the current `.sdlc/config.json` from disk,
locate the guardrail in `<section>.guardrails[]` by the id specified in the
proposal's `patch`, and replace its fields with the proposal's merged values
(description, severity). Do NOT remove fields; do NOT lower severity
(strengthen-only invariant — R8/C9). If no guardrail with the target id exists
in the current file, treat the proposal as malformed and surface to the user.
`consolidate` goes through the same write-then-validate-then-revert flow as 5a.

### 5c. Ambiguous upstream-report offer (R-ambig-offer, issue #288)

When `RESULT.classification === "ambiguous"` AND
`RESULT.errorReportPayload != null`, the orchestrator concluded the failure
*may* be a plugin defect even though the evidence was not strong enough to
classify it as one. After the per-proposal apply/skip flow above completes,
present an opt-in upstream-report offer:

> This failure may also be a plugin defect. File a GitHub issue?

Use AskUserQuestion with options: **invoke error-report-sdlc** | **skip**.

- On `invoke error-report-sdlc`: invoke `error-report-sdlc`, providing the
  fields from `RESULT.errorReportPayload` (Skill/Step/Operation/Error/Suggested
  investigation — same shape and idiom used everywhere else in this plugin).
- On `skip`: record the skip in Step 7 Learning Capture and exit cleanly.

The strengthen-only invariant is preserved — no surface is auto-edited; the
user explicitly approves the dispatch. When `RESULT.errorReportPayload == null`
on `ambiguous` (pure user-code ambiguity), this sub-step is suppressed entirely
— do not surface the prompt.

## Step 6 — PLUGIN-DEFECT ROUTE: Dispatch error-report-sdlc (R9)

When `RESULT.classification == "plugin-defect"`:

1. Display `RESULT.errorReportPayload` to the user as the proposed
   `error-report-sdlc` dispatch payload.
2. Use AskUserQuestion: **invoke error-report-sdlc** | **cancel**.
3. On `invoke error-report-sdlc`: invoke `error-report-sdlc`, providing:
   `skill=<failure.skill>`, `step=<failure.step>`, `operation=<failure.operation>`,
   `error=<failure.text>`, `exitOrHttpCode=<failure.exitCode>`,
   `errorType=<failure.errorType or "script crash">`.
4. Do NOT edit any user-side hardening surface in the plugin-defect path. The
   no-silent-write invariant applies here too — the user must explicitly
   approve the error-report dispatch.

`rm -f "<manifestPath>"` on every exit path from this step.

## Step 7 — Learning Capture

Append a single line to `.sdlc/learnings/log.md` summarizing the hardening
action:

```
## YYYY-MM-DD — harden-sdlc: <classification> for <failure.skill> at <failure.step>
Applied: <count> proposal(s) across <surface-list> | Skipped: <count> | Routed: <yes|no>
AmbiguousOffer: <not-applicable|offered-dispatched|offered-skipped>
Trigger: <first 80 chars of failure.text>
Dimensions: <comma-separated dimension names that were created or modified>
```

The `Dimensions:` line MUST be included **only when `<surface-list>` includes
`review-dimensions`** (i.e., at least one review-dimension file was created or
modified during this hardening run). When `review-dimensions` is NOT in the
surface-list, the `Dimensions:` line MUST be omitted entirely — do not emit it
with an empty value.

This line exists so that plan-sdlc's dimension-coverage gate can
deterministically suppress duplicate dimension proposals on subsequent runs
within the same PR commit window, by grepping the last 100 lines of
`.sdlc/learnings/log.md` for recent `harden-sdlc` entries whose `Dimensions:`
line names the candidate dimension.

The `AmbiguousOffer` line records the Step 5c outcome:

- `not-applicable` — classification was not `ambiguous`, OR was `ambiguous` with
  `errorReportPayload == null` (no plugin evidence; offer suppressed).
- `offered-dispatched` — Step 5c offered the upstream-report and the user chose
  `invoke error-report-sdlc`.
- `offered-skipped` — Step 5c offered the upstream-report and the user chose
  `skip`.

Mirror the append pattern used by `commit-sdlc` and `execute-plan-sdlc`. Create
the `.sdlc/learnings/` directory and `log.md` file if they don't exist.

`rm -f "<manifestPath>"` here if it has not already been removed by an earlier
step (it should have been — this is a defensive final check, not a new
cleanup path).

## DO NOT

- Edit any surface without an `apply` AskUserQuestion answer recorded for that
  specific proposal — the no-silent-write invariant is non-negotiable.
- Accumulate approved changes across multiple proposals and write them together
  — each approved proposal MUST be written to disk immediately before advancing
  to the next proposal (R-iteration-write, issue #387).
- Propose relaxing or removing existing rules — v1 is strengthen-only.
- Run full-suite or wide-subset eval automatically — a single targeted check
  scoped to the change is allowed; tight-loop retries are not.
- Invoke `error-report-sdlc` for `user-code` classifications — only the
  `plugin-defect` branch (Step 6) and the `ambiguous`-with-payload branch (5c)
  route there.
- Read the full manifest contents into the main context — Step 2 reads only
  `failure.*` and `classification_hint`; the orchestrator owns the rest.
- Auto-dispatch this skill from a caller skill without explicit user selection
  in the caller's failure-handling menu.
- Recursively dispatch this skill on its own `harden_prepare` or orchestrator
  crash — log the failure and stop.
- Override severity vocabulary chosen by the orchestrator (R10/R17) — each
  surface has its own canonical vocabulary; never substitute one for the other.
- Leave a schema-invalid edit in place after a failed post-write `validate`
  call — revert per 5a.

## When This Skill Is Invoked

- **Standalone:** `/harden-sdlc --failure-text "..." --skill plan-sdlc --step "Step 5" --operation "reviewer-loop"`
- **Caller-dispatched:** Caller-dispatched skills present an opt-in menu option at their failure surfaces that dispatches `Skill(harden-sdlc)` with the same flag shape. `ship-sdlc` is intentionally NOT a caller — it delegates failure handling to its sub-skills, so harden-sdlc reaches the user through whichever sub-skill failed.

## See Also

- [`/error-report-sdlc`](../error-report-sdlc/SKILL.md) — plugin-defect route
- [`/setup-sdlc`](../setup-sdlc/SKILL.md) — initial guardrail/dimension authoring
