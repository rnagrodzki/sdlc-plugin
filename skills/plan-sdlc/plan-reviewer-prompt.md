# Plan Reviewer Prompt Template

Use this template in plan-sdlc Step 5 (CRITIQUE) when dispatching the plan review subagent.

**Purpose:** Verify the plan is complete, accurate, and ready for execution by execute-plan-sdlc.

**Model selection:** Use a different model than the one that wrote the plan for plans with 5+ tasks (cross-model review catches blind spots). For plans under 5 tasks, same model is acceptable.

## How to Fill This Template

- `{PLAN_FILE_PATH}` — path to the written plan document
- `{REQUIREMENTS_CHECKLIST}` — numbered list from Step 1 (CONSUME)
- `{SOURCE_REQUIREMENTS}` — file path or inline text of the original spec/requirements (if available)
- `{BRIEF_FILE}` — absolute path to `discovery-brief.md` produced by `plan-explore-orchestrator`, or `"none — orchestrator skipped"` when the lightweight path or fallback ran
- `{OPENSPEC_TASKS}` — serialized JSON array from `openspecContext.tasks[]` when `--from-openspec` was active; `"none — plan not from OpenSpec"` otherwise
- `{REQUIREMENTS_JSON}` — JSON array of `{ reqId, capability, type, name, scenarioCount }` from the delta-spec inventory, or `"null"` when unavailable (CLI absent or non-OpenSpec plan). When `{LENS}=all` (single-reviewer, <5 tasks), include this for traceability matrix building if present.
- `{LENS}` — reviewer lens: one of `architecture`, `requirements`, `risk`, or `all`. When `{LENS}=all`, the reviewer evaluates all categories (status quo for plans <5 tasks). When `{LENS}` is one of the three specific values, the reviewer filters evaluation to the categories listed in `{LENS_FOCUS}`.
- `{LENS_FOCUS}` — category subset for the lens, rendered as a bullet list (e.g., `- Buildability\n- Task descriptions\n- Decision documentation\n- Dependency accuracy`). When `{LENS}=all`, set to `"all categories below"`.

**Lens → category mapping (KD3, Fixes #418):**
- `architecture`: Buildability, Task descriptions, Decision documentation, Dependency accuracy
- `requirements`: Requirements coverage, Metadata completeness, Plan completeness, OpenSpec G16, Exploration provenance, Best-practice traceability
- `risk`: File paths, Verification strategy, Scope discipline, Guardrail compliance
- `all`: all categories (single-reviewer mode for plans <5 tasks — preserves today's behavior)

```
Task tool (general-purpose):
  description: "Plan review for <feature name>"
  model: <reviewer model — different from plan author model when 5+ tasks>
  mode: bypassPermissions
  prompt: |
    You are reviewing a plan document for completeness, accuracy, and executability.
    The plan will be executed by an automated plan orchestrator (execute-plan-sdlc).

    **Plan to review:** {PLAN_FILE_PATH}
    **Source requirements:** {SOURCE_REQUIREMENTS — file path or inline text, or "not provided"}
    **Requirements checklist:**
    {REQUIREMENTS_CHECKLIST}
    **Plan guardrails:** {GUARDRAILS — one per line, or "none configured"}
    **Discovery brief:** {BRIEF_FILE — absolute path to discovery-brief.md, or "none — orchestrator skipped"}
    **openspecContext.tasks:** {OPENSPEC_TASKS — serialized JSON array from prepare output, or "none — plan not from OpenSpec"}
    **Requirement inventory:** {REQUIREMENTS_JSON — JSON array from openspec show --json --deltas-only, or "null"}

    **Focus areas (this lens only):** {LENS_FOCUS}
    (When lens is `all`, evaluate all categories below. When lens is `architecture`, `requirements`, or `risk`, evaluate only the categories listed in Focus areas above — skip all other categories.)

    ## What to Check

    Read the plan file. For each check, verify by reading the plan — do not assume.

    Skip `## OpenSpec Appendix` content when evaluating any gate.

    | Category | What to Look For |
    |---|---|
    | Requirements coverage | Every requirement in the checklist has at least one task; no orphan tasks without a requirement |
    | Task descriptions | Each task is specific enough to implement without guessing — exact file paths, clear behavior, edge cases |
    | Metadata completeness | Every task has Complexity, Risk, Depends on, and Verify fields |
    | Dependency accuracy | Dependencies are correct; no circular deps; implicit deps (barrel files, type exports) are captured |
    | File paths | All paths are relative to project root (no absolute paths) |
    | Verification strategy | Each task has an appropriate verification method (not TDD forced on config/docs tasks) |
    | Scope discipline | No tasks beyond stated requirements; no gold-plating |
    | Buildability | An agent with no codebase context could execute each task using only its description |
    | Decision documentation | Key Decisions section present for plans with 5+ tasks; each rationale references codebase evidence, not preference; no obvious decisions included |
    | Plan completeness | All header fields (Goal, Architecture, Source, Verification) are filled in — no placeholders like "[TBD]"; no leftover working sections (e.g., `## Requirements` scaffolding); task numbering is sequential with no gaps |
    | Guardrail compliance | Each guardrail from the guardrails list above is satisfied by the plan. Error-severity violations are blocking. Warning-severity violations are advisory. If no guardrails configured, skip this check. |
    | Exploration provenance | When `{BRIEF_FILE}` is provided (not "none"): every Standard/Complex task in the plan cites ≥1 `F-<DIM>-<n>` finding ID OR is marked "out-of-scope addition" with rationale. Trivial tasks exempt. Flag uncited Standard/Complex tasks as a blocking issue (G15). |
    | Best-practice traceability | When the brief contains a `## Best-Practice Synthesis` section: Key Decisions explicitly ADOPTS / REJECTS-with-rationale / marks NOT-APPLICABLE each web finding by `F-<DIM>-<n>` ID. Silent omission of a web finding is a blocking issue. |
    | **OpenSpec tasks.md coverage (G16)** | When the plan was generated with `--from-openspec` AND `openspecContext.tasks[]` is provided in the input data: every entry in `openspecContext.tasks[]` is either (a) referenced by ≥1 plan task's `openspec-task.ref`, OR (b) listed in `## Out-of-scope OpenSpec tasks`. Each `openspec-task` block must have all four fields (`change`, `ref`, `line`, `title`) populated. Flag any uncovered task (not referenced AND not out-of-scope) as a blocking issue (G16). When the plan was NOT generated from OpenSpec (no `openspecContext.tasks` in the input), skip this check entirely. |

    ## Calibration

    Only flag issues that would cause real problems during execution:
    - A task so vague an agent would hallucinate → flag it
    - A missing dependency that would cause a wave conflict → flag it
    - A wrong file path → flag it
    - A stylistic preference about task ordering → do NOT flag

    Approve unless there are genuine execution blockers within your focus areas.

    ## Output

    **Status:** Approved | Issues Found

    **Issues (if any — list only execution blockers within your focus areas):**
    - Task N: [specific issue] — [why this would cause execution failure]

    **Recommendations (advisory, do not block approval):**
    - [optional suggestions that improve the plan but are not blocking]
```

## Handling Reviewer Output

**Single reviewer (`{LENS}=all`, plans <5 tasks):**
- Approved → Proceed to Step 7 (Handoff)
- Issues Found → Fix each blocking issue in the plan document; re-dispatch the reviewer; maximum 3 reviewer iterations — if still unresolved, surface to user

**Multi-lens reviewer (plans ≥5 tasks — merge in Step 5):**
- Main agent merges lens results: Status = `Approved` iff ALL lenses approved; Issues = union of blocking issues (dedup by taskRef + message-prefix); Recommendations = prefix-dedup
- Merged Approved → Proceed to Step 7
- Merged Issues Found → Fix blocking issues; re-dispatch all lenses in next iteration; maximum 3 iterations total
