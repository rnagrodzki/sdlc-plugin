# G17 Dimension Coverage Prompt Template

Use this template in plan-sdlc Step 3 when dispatching the G17 Dimension Coverage subagent.

**Purpose:** Detect coverage gaps in the active review-dimension catalog and in the Copilot mirror at `.github/instructions/`, given a finalized plan file. Emit structured findings for Step 4 to splice as a `## Suggested Review Dimensions` advisory block.

**Model:** sonnet (from `g17Dispatch.model` in prepare output — do NOT hardcode)

**Dispatch parameters (from prepare output — `agent-dispatch-script-driven` guardrail):**
- `subagent_type`: `g17Dispatch.subagentType`
- `model`: `g17Dispatch.model`
- prompt body: fill the template variables below from prepare output and plan context

```
Task tool (general-purpose):
  description: "G17 Dimension Coverage analysis for <plan title>"
  model: <g17Dispatch.model from prepare output>
  mode: bypassPermissions
  prompt: |
    You are the Dimension Coverage subagent (G17). Your job is to detect coverage
    gaps in the active review-dimension catalog AND in the Copilot mirror at
    `.github/instructions/`, given a finalized plan file. You emit structured
    findings that plan-sdlc Step 4 splices into the plan as a
    `## Suggested Review Dimensions` advisory block.

    **This gate is advisory (non-blocking).** Never fail or halt plan finalization.
    If you cannot complete any step, emit empty findings and explain briefly.

    ## Inputs

    - Plan file path: {PLAN_FILE_PATH}
    - Dimensions directory: {DIMENSIONS_DIR} (`.sdlc/review-dimensions/`)
    - Copilot instructions directory: {COPILOT_DIR} (`.github/instructions/`)
    - GitHub hosting detected: {GITHUB_HOSTING_DETECTED} (boolean from P14 — do NOT re-derive)
    - Learnings log path: {LEARNINGS_LOG_PATH} (`.sdlc/learnings/log.md`)
    - PR commit window: {PR_COMMIT_WINDOW} (e.g., "last 14 days" — best-effort)

    Skip `## OpenSpec Appendix` content when evaluating any gate.

    ## Process (execute in this order)

    ### Step A — Read the plan file

    Read `{PLAN_FILE_PATH}`. Extract:
    - All `Files: Create:` and `Files: Modify:` paths → **plan path set**
    - All task `Description` blocks → **behavior-text set**

    ### Step B — Read dimension catalog

    Read every `*.md` file in `{DIMENSIONS_DIR}`. For each, parse:
    - Frontmatter: `name`, `triggers[]`, `severity`, `skip-when[]`
    - Body: keywords and checklist items

    Build a `dimensionName → { triggers, severity, body }` map.
    If the directory does not exist or is empty, proceed with an empty map.

    ### Step C — Read Copilot mirror catalog

    Read every `*.instructions.md` file in `{COPILOT_DIR}`. For each, parse:
    - Frontmatter `applyTo` field
    - Body description (first paragraph)

    Build a `dimensionName → hasMirror` map by matching instruction file names
    to dimension names (strip `.instructions.md` suffix; kebab-match).
    If the directory does not exist or is empty, proceed with an empty map.

    ### Step D — Match plan paths to dimensions

    For each path in the plan path set, match against every dimension's `triggers[]`
    using minimatch glob semantics. Record which dimensions have ≥1 matching path
    (covered) and which have no match (uncovered paths).

    ### Step E — Evaluate CREATE criteria (C1–C6)

    Apply these criteria to uncovered paths (paths that matched no dimension trigger):

    - **C1**: Path introduces a new top-level technology directory (e.g., `terraform/`, `k8s/`, `mobile/`). Severity: medium.
    - **C2**: 3+ new files share a common path prefix not yet covered by any dimension. Severity: medium.
    - **C3**: Path matches a security-sensitive pattern (`**/auth/**`, `**/secret*`, `**/cred*`, `**/iam/**`, `**/crypto/**`, `**/pii/**`). Severity: high.
    - **C4**: Path matches infrastructure/deployment patterns (`**/infra/**`, `**/terraform/**`, `**/*.tf`, `**/k8s/**`, `**/helm/**`, `**/docker*`, `**/Dockerfile*`). Severity: critical.
    - **C5**: 5+ files across multiple uncovered directories. Severity: medium.
    - **C6**: Single file, not matching C3/C4. Severity: low.

    Suppression rule: do NOT fire C6 for single-file diffs. For C1/C2/C5, require ≥2 files.

    ### Step F — Evaluate UPDATE-path criteria (U1–U6)

    Apply to covered dimensions (paths matched) but where trigger globs may be stale:

    - **U1**: Plan files have a different extension than what the dimension's triggers glob (e.g., dimension triggers `src/**/*.js` but plan adds `src/**/*.ts`). Severity: medium.
    - **U2**: Plan files are in a subdirectory not matched by existing trigger globs (e.g., trigger is `src/**` but files land in `packages/*/src/**`). Severity: medium.
    - **U3**: Plan renames or moves a directory that a dimension trigger explicitly names. Severity: high.
    - **U4**: Plan adds a new file extension to a path family the dimension covers (e.g., adds `.mjs` to a JS-only dimension). Severity: low.
    - **U5**: Trigger glob uses `**` but new files are outside the wildcard scope. Severity: medium.
    - **U6**: Plan adds ≥3 files to a path family where the dimension's trigger is a specific filename (not a glob). Severity: medium.

    Suppression rule: do NOT fire UPDATE criteria for doc-only diffs where ALL plan paths match `docs/**`, `README*`, or `*.md` files outside the `plugins/sdlc-utilities/skills/` directory.

    ### Step G — Evaluate UPDATE-behavior criteria (B1–B4)

    Apply to each task Description in the behavior-text set, matching against covered dimensions:

    - **B1**: Description indicates a public API, CLI flag, environment variable, webhook, hook, or frontmatter contract change. Severity: high.
    - **B2**: Description references authentication, cryptography, PII, IAM, secrets, or session management — and a `security`-type dimension exists. Severity: high.
    - **B3**: Description indicates an invariant flip: sync↔async, idempotent↔non-idempotent, atomic↔multi-step, blocking↔non-blocking. Severity: critical.
    - **B4**: Description adds or removes a runtime dependency (new `require()`, import, npm package) that changes the module surface. Severity: medium.

    **Pure-refactor suppression**: Do NOT fire B-criteria when the Description explicitly indicates rename-only, formatting, type-narrowing without semantic change, or dead-code removal. Look for markers like "rename-only", "no behavior change", "pure refactor", "formatting only".

    ### Step H — Apply suppression and ranking

    1. Drop CREATE proposals for single-file diffs unless C3 or C4 fired.
    2. Drop UPDATE proposals for doc-only diffs (ALL paths match `docs/**`, `README*`, or `*.md` outside skills).
    3. Drop B-criterion proposals for pure refactors (rename-only, formatting, type-narrowing).
    4. Rank surviving proposals by: `severity_rank DESC, criteria_count DESC, dimension_name ASC`
       where `severity_rank = { critical: 4, high: 3, medium: 2, low: 1, info: 0 }`.
    5. Cap at 3 proposals. Record suppressed count.

    ### Step I — Defer check against harden-sdlc learnings

    Read the last 100 lines of `{LEARNINGS_LOG_PATH}` (if it exists; skip if absent).
    Parse `## YYYY-MM-DD — harden-sdlc:` headers within `{PR_COMMIT_WINDOW}`.
    For each such header, look for a `Dimensions:` line on the immediately following lines.
    Extract the comma-separated dimension names from that line.

    Drop any surviving proposal whose `dimension` name appears in a recent
    harden-sdlc entry's `Dimensions:` list. This prevents duplicate proposals
    within an active PR window where harden-sdlc already proposed the same dimension.

    ### Step J — Attach X1 Copilot-mirror sync clause

    For each surviving proposal:
    - If `{GITHUB_HOSTING_DETECTED}` is `true`:
      - For CREATE proposals: always attach X1 (a new dimension needs a new mirror).
      - For UPDATE proposals: attach X1 only if the dimension already has a mirror
        in the Copilot catalog built in Step C.
    - If `{GITHUB_HOSTING_DETECTED}` is `false`: omit X1 entirely.

    X1 action line MUST reference `setup-sdlc/setup-dimensions.md` Step 8 transform
    by path — do NOT inline the transform. Example action:
    `regenerate .github/instructions/<name>.instructions.md per setup-sdlc/setup-dimensions.md Step 8 transform`

    ## Output Schema

    Emit a single fenced ` ```json ` block containing exactly this schema:

    ```json
    {
      "findings": [
        {
          "kind": "CREATE | UPDATE-path | UPDATE-behavior",
          "dimension": "<kebab-name>",
          "criteria": ["C1", "C2"],
          "severity_hint": "critical | high | medium | low | info",
          "why": "<one-sentence rationale citing concrete plan evidence>",
          "triggers": ["<glob>"],
          "patch": "<one-line patch description>",
          "actions": {
            "dimension": "<exact CLI command or edit instruction>",
            "copilot_mirror": "<regenerate instruction OR null>"
          }
        }
      ],
      "suppressed_count": 0,
      "rendering": "<full markdown of the Suggested Review Dimensions section, ready to splice>"
    }
    ```

    Field rules:
    - `triggers`: present only on CREATE proposals (omit on UPDATE).
    - `patch`: present only on UPDATE proposals (omit on CREATE).
    - `copilot_mirror`: `null` when X1 is suppressed (no GitHub remote, or no existing mirror for UPDATE).
    - `rendering`: the full markdown block ready to append to the plan file. Use H3 blocks:
      - CREATE: `### CREATE: <kebab-name>`
      - UPDATE: `### UPDATE: <kebab-name> (<criteria joined with ", ">)`
      Each block contains: **Why**, **Severity hint**, **Triggers** (CREATE only), **Patch** (UPDATE only), **Action (dimension)**, **Action (Copilot mirror, X1)** (omit last line when `copilot_mirror` is null).
    - When `findings` is empty, `rendering` MUST be `""` (empty string).
    - When suppressed_count > 0, append `_N additional candidates suppressed_` (italic) as the last line of `rendering`.

    ## DO NOT

    - Execute the Copilot transform inline — reference `setup-sdlc/setup-dimensions.md` Step 8 by path only.
    - Propose REMOVING any checklist item from an existing dimension (strengthen-only invariant).
    - Emit absolute GitHub blob URLs in action lines (use relative paths or slash commands only).
    - Block plan finalization — this gate is advisory; absent proposals are not failures.
    - Re-derive `{GITHUB_HOSTING_DETECTED}` — consume the value as provided (`flag-resolution-single-source`).
    - Include volatile bytes (timestamps, run IDs, random values) in the static section of your output — cache-stability requirement.

    ## Self-check (before returning)

    1. Verify the rendered markdown section uses only H3 blocks (`### CREATE:` / `### UPDATE:`), not tables.
    2. Verify `actions.dimension` values use slash commands or relative paths — no absolute GitHub blob URLs.
    3. Verify `triggers` is omitted on UPDATE proposals and `patch` is omitted on CREATE proposals.
    4. Verify the JSON is valid (balanced braces, no trailing commas).
    5. Verify `suppressed_count` matches the actual number of proposals dropped in Step H.
    6. If `{GITHUB_HOSTING_DETECTED}` is false, verify no proposal has a non-null `copilot_mirror`.
```

## Handling G17 Subagent Output

After the subagent returns, in main context:

1. Parse the `WAVE_SUMMARY`-style JSON from the subagent's response (or the `findings` JSON block directly).
2. Store the parsed result as `g17Findings` in memory.
3. **On dispatch failure, timeout, or malformed JSON:** treat `g17Findings` as `{ findings: [], rendering: "", suppressed_count: 0 }`. Log the failure to `.sdlc/learnings/log.md`:
   ```
   ## YYYY-MM-DD — plan-sdlc: G17 dispatch failed — <error summary>
   ```
   Continue to Step 4. Never block plan finalization on a G17 fault (R31).
4. Ensure G17 has returned and `g17Findings` is populated **before** writing the `critiqueRan` marker.

## Template Variable Reference

| Variable | Source | Notes |
|---|---|---|
| `{PLAN_FILE_PATH}` | Plan file path resolved in Step 0/3 | Absolute path |
| `{DIMENSIONS_DIR}` | `.sdlc/review-dimensions/` | Relative to project root |
| `{COPILOT_DIR}` | `.github/instructions/` | Relative to project root |
| `{GITHUB_HOSTING_DETECTED}` | `githubHosting.detected` from P14 | Boolean — never re-derive |
| `{LEARNINGS_LOG_PATH}` | `.sdlc/learnings/log.md` | May not exist; handle gracefully |
| `{PR_COMMIT_WINDOW}` | Best-effort — "last 14 days" if unknown | String for context only |
