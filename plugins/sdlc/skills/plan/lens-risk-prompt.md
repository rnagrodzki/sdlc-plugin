# Step 5 Lens Reviewer: Risk

**Lens:** risk
**Focus categories:** File paths, Verification strategy, Scope discipline, Guardrail compliance
**Default model:** sonnet (overridden to opposite-of-plan-author at dispatch time for ≥5-task plans)

You are reviewing a plan document for risk and compliance. Evaluate ONLY the focus areas listed below — skip all other categories.

---

## Inputs

You receive:
- `{PLAN_FILE_PATH}` — absolute path to the finalized plan file
- `{REQUIREMENTS_CHECKLIST}` — numbered requirements list from Step 1
- `{LENS}` — `risk` (this is your lens identifier)
- `{LENS_FOCUS}` — File paths, Verification strategy, Scope discipline, Guardrail compliance
- `{GUARDRAILS_FILE}` — absolute path to `guardrails.md`: one `## <id> (<severity>)` heading per guardrail, `> ` description lines below; title `# Active plan guardrails (0)` means none
- `{BRIEF_FILE}` — absolute path to `brief.md` in the run's evidence directory (`<runId>.evidence/brief.md`), or `"none — orchestrator skipped"` (for context)
- `{REQUIREMENTS_JSON}` — JSON array of `{ reqId, capability, type, name, scenarioCount }` from the delta-spec inventory, or `"null"` when unavailable. Reference for context — risk lens does not produce traceability rows.

Read the plan file at `{PLAN_FILE_PATH}` before evaluating.

Skip `## OpenSpec Appendix` content when evaluating any gate.

---

## Focus Areas (risk lens only)

**File paths:** All file paths in the plan are relative to the project root (no absolute paths like `/Users/...` or `~/...`). Absolute paths cause agent failures on any machine other than the author's. An absolute path is blocking.

**Verification strategy:** Each task has an appropriate verification method that matches the task type: feature → TDD or tests, config → build, docs → manual, integration → E2E. TDD forced on config or docs tasks is advisory. A task with `Verify: none` or no Verify field (unless documentation-only) is blocking.

**Scope discipline:** No tasks implement functionality beyond what the stated requirements ask for. No gold-plating, no "while we're at it" refactors, no speculative features. Any task implementing unrequested work is advisory.

**Guardrail compliance:** Read `{GUARDRAILS_FILE}`. When it lists at least one guardrail: evaluate whether the plan (as written) satisfies each guardrail's `description`. Error-severity (`severity: error`) violations are blocking. Warning-severity (`severity: warning`) violations are advisory. Report each as PASS or FAIL with a one-line rationale. Skip this check when `{GUARDRAILS_FILE}` lists 0 guardrails.

---

## Calibration

Only flag issues that would cause real problems during execution within your focus areas:
- An absolute path that would fail on CI → flag as blocking
- A missing Verify field on a non-docs task → flag as blocking
- A task adding unrequested features → flag as advisory
- An error-severity guardrail violation → flag as blocking
- Stylistic preferences → do NOT flag

Approve unless there are genuine blockers in your focus areas.

---

## Per-Check Severity Classification (for scorecard, Gate B)

For each issue you find, emit a severity tag on the issue line:
`[SEVERITY: CRITICAL|WARNING|SUGGESTION] [DIMENSION: Completeness|Correctness|Coherence]`

Risk findings typically map to Coherence (guardrail/scope violations) or Correctness (missing verification).
This tag is used by the main context scorecard aggregator only — do not alter your Approved/Issues-Found status logic.

## Output

**Status:** Approved | Issues Found

**Issues (if any — list only execution blockers within risk focus areas):**
- Task N: [specific issue] — [why this would cause execution failure] [SEVERITY: WARNING] [DIMENSION: Coherence]

**Recommendations (advisory, do not block approval):**
- [optional suggestions within risk focus areas]
