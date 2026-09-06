# Spec Compliance Reviewer Prompt Template

Use this template in Step 5c-bis when dispatching the per-wave spec compliance reviewer. Dispatch as a single agent (sonnet) after mechanical verification passes for the wave.

**Purpose:** Verify that agents built what was requested — nothing more, nothing less.

**When to use:** After every wave containing Standard or Complex tasks, unless the full preset (Speed) was selected.

**When to skip:** Wave contains only Trivial tasks, or the full preset (Speed) was selected.

## How to Fill This Template

For `{TASK_LIST}`, list each non-trivial task's:
- Full specification text (copy from the plan)
- Files the agent's completion checklist listed as modified

For `{WAVE_NUMBER}`, use the current wave number.

```
Task tool (general-purpose):
  description: "Spec compliance review for Wave {WAVE_NUMBER}"
  model: sonnet
  mode: bypassPermissions
  prompt: |
    You are reviewing whether implementations match their specifications. Read the actual
    code — do not trust agent reports.

    ## Tasks to Review

    {TASK_LIST — for each non-trivial task in the wave:}
    ### Task N: [Task Name]
    **Specification (full text from plan):**
    [PASTE FULL TASK REQUIREMENTS]

    **Files the agent claimed to modify:**
    - path/to/file1
    - path/to/file2

    ---
    {repeat for each task}

    ## OpenSpec Delta Specs (when available)

    {OPENSPEC_DELTA_SPECS — if openspecSpecs was loaded in Step 1, paste the full content
    of each delta spec file here. If not available, omit this entire section.}

    When delta specs are provided, verify that implementations satisfy BOTH the task
    acceptance criteria AND the OpenSpec delta spec requirements. A task may pass its own
    criteria but violate a broader delta spec requirement — flag those cases specifically.
    Pay special attention to ADDED requirements (must have new code), MODIFIED requirements
    (must reflect the change), and REMOVED requirements (must not have active code paths).

    ## CRITICAL: Do Not Trust Agent Reports

    Agents may have incomplete, inaccurate, or optimistic completion reports.

    **DO NOT:**
    - Take their word for what they implemented
    - Accept their STATUS: DONE as proof of correctness
    - Trust their interpretation of requirements

    **DO:**
    - Read the actual code in the files listed above
    - Compare actual implementation to requirements line by line
    - Check for missing pieces the agent claimed to implement
    - Look for extra features the agent didn't mention

    ## Your Job

    For each task, read the code and verify:

    **Missing requirements:**
    - Did they implement everything requested?
    - Are there requirements skipped or partially implemented?
    - Did they claim something works but not actually implement it?

    **Extra/unneeded work:**
    - Did they build things that were not requested?
    - Did they over-engineer or add unnecessary features?

    **Misunderstandings:**
    - Did they interpret a requirement differently than intended?
    - Did they solve a different problem than specified?

    Verify by reading code. Do not trust reports.

    ## Output Format

    For each task:
    - ✅ Task N: [task name] — Spec compliant
    - ❌ Task N: [task name] — Issues: [specific missing/extra items with file:line references]

    **Overall verdict:**
    - ✅ Wave spec compliant — all tasks match their specifications
    - ❌ Issues found — N task(s) need fixes: [summary]
```

## Handling Reviewer Findings

**If ✅ Wave spec compliant:** proceed to Step 5d (progress report).

**If ❌ Issues found:**
- 1–2 minor issues → fix inline in main context
- Major spec gaps → re-dispatch the original implementer agent with specific fix instructions
  (counts toward that task's 2-retry budget)
- After fixes, re-dispatch the spec compliance reviewer to confirm the issues are resolved
