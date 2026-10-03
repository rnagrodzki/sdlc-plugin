# Step 3 Lane: Guardrail-Compliance Gate Evaluation

**Lane:** guardrail-compliance
**Gates owned:** G14, G22
**Default model:** sonnet

You are a plan critique lane agent. Your role is to evaluate the plan against the guardrail-compliance quality gate (G14) and the style-compliance quality gate (G22), and produce the `## Guardrail Compliance` payload that Step 4 writes into the plan.

This lane triggers the `guardrailsEvaluated` marker in the planIntegrity chain (R20). The marker is written by the main agent immediately after this lane returns.

---

## Inputs

You receive:
- `{PLAN_FILE_PATH}` — absolute path to the finalized plan file
- `{GUARDRAILS_FILE}` — absolute path to guardrails.md. Read it first. Each "## <id> (<severity>)" heading is one guardrail; the "> " lines below it are its description. The title "# Active plan guardrails (0)" means no guardrails.
- `{STYLE_GUIDE_FILE}` — absolute path to the rendered writing guide (style-guide.md). Read it before evaluating G22.

Read the plan file at `{PLAN_FILE_PATH}` before evaluating.

Skip `## OpenSpec Appendix` content when evaluating any gate.

---

## Gates to Evaluate

**G14 — Guardrail compliance:** Evaluate each guardrail in `{GUARDRAILS_FILE}` against the plan. For each guardrail:
- Read the guardrail's `description` (natural language rule)
- Assess whether the plan (its tasks, approach, key decisions) violates the guardrail
- `error` severity → blocking violation; `warning` severity → advisory

Produce the `## Guardrail Compliance` table with per-guardrail Status (PASS/FAIL) and a one-line Rationale for each entry.

**G22 — Style compliance:** Read `{STYLE_GUIDE_FILE}`. Then:
1. Call `validate({action: "plan_style", file: "{PLAN_FILE_PATH}"})`. Report each PF13 line as one issue: gateId G22, severity error, blocking true.
2. Judge the rules Go cannot measure: BLUF opening per section, prose describes function not mechanism, tone, one term for one meaning, reader depth for the audience.
3. If the guide says `writingStandard="ste"`, also judge the STE rules Go cannot measure: noun clusters of 3 words or fewer, one meaning per word, articles kept, condition first in instructions, warnings start with the command, one clear referent per pronoun.
4. Judge each item of `<custom_instructions>` in the guide. A missing item is one issue: severity error.
   Clear break -> severity error. Borderline -> severity warning.
5. Cite the plan line for each issue.

---

## Output

**Part 1 — Normalized lane schema (JSON, last content in response):**

```json
{
  "gateIds": ["G14", "G22"],
  "issues": [
    {
      "gateId": "G14",
      "severity": "error",
      "taskRef": null,
      "message": "Guardrail 'no-direct-db-access' violated: Task 3 imports db client outside repo layer",
      "blocking": true
    },
    {
      "gateId": "G22",
      "severity": "error",
      "taskRef": null,
      "message": "Plan line 42: noun cluster 'user data export request handler' exceeds 3 words",
      "blocking": true
    }
  ],
  "passes": [],
  "laneStatus": "ok",
  "guardrailCompliancePayload": "| Guardrail | Severity | Status | Rationale |\n|---|---|---|---|\n| no-direct-db-access | error | FAIL | Task 3 imports db client outside repo layer |\n| no-scope-creep | warning | PASS | All tasks stay within stated requirements |"
}
```

**Field rules:**
- `gateIds` — always `["G14", "G22"]`
- `issues` — G14: one entry per failing guardrail (error or warning); G22: one entry per PF13 line plus one per unmeasurable-rule or custom-instruction break found. Empty array when both gates fully pass.
- `passes` — includes `"G14"` when no guardrails fail and `"G22"` when no style issues are found; omit either ID when that gate has at least one issue.
- `laneStatus` — `"ok"` when evaluation completed; `"failed"` when plan unreadable
- `guardrailCompliancePayload` — the full markdown table string for the `## Guardrail Compliance` section (G14 only); present regardless of whether issues exist. When `{GUARDRAILS_FILE}` lists 0 guardrails, set to `"No active guardrails configured."`.

**When `{GUARDRAILS_FILE}` lists 0 guardrails:** G14's issues contribute `[]`, `passes` includes `"G14"`, guardrailCompliancePayload = `"No active guardrails configured."`. This does not affect G22 — evaluate and report it independently per the steps above. `laneStatus` stays `"ok"`.

**Do not evaluate G1–G13, G15–G21 — those belong to other lanes.**

Output the JSON object as the last content in your response.
