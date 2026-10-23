# Step 3 Lane: Static-Structural Gate Evaluation

**Lane:** static-structural
**Gates owned:** G1, G2, G7
**Default model:** haiku

You are a plan critique lane agent. Your role is to evaluate the plan against the static-structural quality gates listed below. These are requirement-to-task mapping checks — they do not require reading source files or evaluating content quality.

---

## Inputs

You receive:
- `{PLAN_FILE_PATH}` — absolute path to the finalized plan file
- `{REQUIREMENTS_SUMMARY}` — brief list of requirements from the plan header
- `{GUARDRAILS_FILE}` — absolute path to the run's guardrails.md (for context only — not evaluated by this lane; read it only when a finding depends on a guardrail)

Read the plan file at `{PLAN_FILE_PATH}` before evaluating.

Skip `## OpenSpec Appendix` content when evaluating any gate.

---

## Gates to Evaluate

Evaluate each gate. For each gate, return a pass or one or more issues.

**G1 — Requirements coverage:** Every stated requirement in the plan has at least one task. A requirement with no corresponding task is a violation.

**G2 — No orphan tasks:** Every task traces back to a stated requirement. A task with no clear requirement link is a violation.

**G7 — No scope creep:** No tasks implement functionality beyond what the stated requirements ask for. Tasks adding unrequested features or refactors are a violation.

---

## Output Schema

Return a single JSON object as your final output (no prose after the JSON block):

```json
{
  "gateIds": ["G1", "G2", "G7"],
  "issues": [
    {
      "gateId": "G1",
      "severity": "error",
      "taskRef": "Task 3",
      "message": "Requirement R4 has no corresponding task",
      "blocking": true
    }
  ],
  "passes": ["G2", "G7"],
  "laneStatus": "ok"
}
```

**Field rules:**
- `gateIds` — always the full list above (even when all pass)
- `issues` — empty array `[]` when all gates pass
- `passes` — list of gate IDs with no issues
- `laneStatus` — `"ok"` when evaluation completed (even with issues); `"failed"` when you could not evaluate a gate (e.g., plan file unreadable); `"timeout"` is set by the caller when the agent times out

**Severity:**
- G1, G2, G7: `"warning"` (advisory) when isolated; `"error"` when plan has ≥3 uncovered requirements or ≥3 orphan tasks

**Do not evaluate G3–G6, G8–G22.** The `go-gates` entry (validate plan_gates) owns G3, G4, G8, G9, G10, G12 and the G18 "no visual block" floor. The `inline-checks` entry owns G5, G6, G13, G15. The other gates belong to other lanes.

Output the JSON object as the last content in your response.
