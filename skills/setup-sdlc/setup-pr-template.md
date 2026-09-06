# PR Template Sub-Flow

Project-aware PR template creator sub-flow: receives scan results from parent, proposes a
tailored template, guides the user through customization section by section, then writes
`.sdlc/pr-template.md`.

> **Port Notes** (Task 44 KD9 rewrite):
> - `validate-pr-template.js`'s exit-code trichotomy (0 pass / 1 validation-fail / 2 crash)
>   collapses onto `validate({ action: "pr_template" })`: empty `findings` = pass;
>   non-empty `findings` = validation-fail (recoverable, no error-report); the tool
>   call itself erroring = crash-equivalent (route to error-report-sdlc).
> - Step 6 below writes with the Write tool directly to `.sdlc/pr-template.md`
>   (which creates `.sdlc/` automatically if needed) — source's `mkdir -p
>   .claude` was a stale leftover from before the `.claude/` → `.sdlc/`
>   migration and is dropped.

---

## Scan Input

This sub-flow expects the parent to provide:

- **GitHub PR template content** (if `.github/PULL_REQUEST_TEMPLATE.md` exists): sections and
  structure
- **Recent PR patterns**: heading patterns from the last 5 PRs (if available via `gh pr list`)
- **Manifest signals**: project type (library, application, service, monorepo) from
  `package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml`, `pom.xml`
- **JIRA evidence**: detected JIRA patterns from branch name, recent commits, or manifests
- **Existing template** (if present): current `.sdlc/pr-template.md` sections and content

---

## Arguments

None — this sub-flow takes no arguments.

---

## Workflow

### Step 2 (PLAN) — Draft Template Proposal

Using the scan signals, draft a proposed `.sdlc/pr-template.md`.

**Starting point:**

- If `.sdlc/pr-template.md` already exists: start from its sections
- Otherwise: start from the default 8-section template:
  1. Summary
  2. JIRA Ticket
  3. Business Context
  4. Business Benefits
  5. Technical Design
  6. Technical Impact
  7. Changes Overview
  8. Testing

**Adaptation rules (apply in order):**

1. **GitHub template merge**: If `.github/PULL_REQUEST_TEMPLATE.md` has sections not in the
   starting template, propose adding them. Cite the GitHub template as evidence.
2. **Recent PR patterns**: If 3+ of the last 5 PRs consistently include a `##` heading not
   in the starting template, propose adding that section.
3. **Remove JIRA section**: If no JIRA evidence was found anywhere, propose removing the
   "JIRA Ticket" section and include an explanatory note.
4. **Rename/merge**: If the GitHub template or recent PRs use different names for similar
   sections (e.g. "What Changed" vs "Changes Overview"), prefer the project's convention.
5. **Custom sections**: If a section appears in the GitHub template or recent PRs with
   unique project content (e.g. "Deployment Steps", "Migration Notes", "QA Checklist"),
   include it in the proposal.

**Format for every section in the proposed template:**

```markdown
## Section Name
[Fill instruction — what the LLM should write here, 1-3 sentences]
```

Fill instruction requirements:

- Specific enough to guide the LLM (not just "write about X")
- Uses project terminology found during the scan
- Between 20 and 200 characters

---

### Step 3 (CRITIQUE) — Evaluate Proposal

Self-review the drafted template before presenting it to the user:

- **Completeness**: Does every section have a fill instruction of at least 20 characters?
- **No duplicates**: Do any two sections cover the same ground? If so, merge them.
- **Evidence**: Is every added or removed section backed by a concrete scan signal?
  Remove speculative sections with no supporting evidence.
- **Fill quality**: Are the fill instructions actionable? Replace vague ones
  ("write about X") with specific guidance
  (e.g. "Describe the API contract changes and any migration steps for consumers").
- **JIRA consistency**: If "JIRA Ticket" was removed, is there a note explaining why?
  If kept, does the fill instruction read:
  "Auto-detected from branch/commits, e.g. PROJ-123. 'Not detected' if no ticket found."?

---

### Step 4 (IMPROVE) — Refine Proposal

Based on the critique:

- Add missing fill instructions
- Merge duplicate sections into one
- Remove speculative sections that lack evidence
- Replace vague fill instructions with specific, actionable guidance

---

### Step 5 (DO) — Present and Customize

Present the refined template in a readable format. Show each section heading and its fill
instruction.

Use AskUserQuestion to present the template and ask:
> Accept this PR template?

Options:
- **accept** — write this template as-is to .sdlc/pr-template.md
- **edit** — tell me which sections to add, remove, rename, or modify
- **section N** — change a specific section (tell me the number)

If the user chooses **edit** or **section N**: make the requested changes and present the
updated template again. Loop until the user says **accept**.

---

### Step 6 — Write Template File

After the user accepts:

1. Write the accepted template to `.sdlc/pr-template.md` (Write tool — creates
   `.sdlc/` automatically if it does not already exist).

2. Confirm success:

   ```text
   Written to .sdlc/pr-template.md

   This template will be used by /pr-sdlc for all future PRs on this project.
   To update it, run /setup-sdlc --pr-template again.
   ```

---

### Step 7 — Validate

After writing:

```
validate({ action: "pr_template" }) → { findings }
```

- `findings` empty (validation **passes**): show a one-line success summary.
- `findings` non-empty (validation **fails**): show the findings and offer to fix them automatically.
- Tool call errors (infra failure, crash-equivalent): show the error and invoke error-report-sdlc.

---

## Quality Gates

Before marking complete, verify:

- Template has at least one `## Section` heading
- Every section has a fill instruction of at least 20 characters
- No duplicate section headings
- File was written successfully to `.sdlc/pr-template.md`
- `validate({ action: "pr_template" })` returned no findings

---

## Error Recovery

> **Flow**: detect → diagnose → auto-recover (retry once if transient) → invoke `error-report-sdlc` for persistent actionable failures.

| Error | Recovery | Invoke error-report-sdlc? |
|-------|----------|---------------------------|
| `validate` tool call errors (infra failure) | Show the error, stop | Yes |
| `validate` returns findings (validation fails) | Show findings, offer auto-fix | No — recoverable |

When invoking `error-report-sdlc`, provide:
- **Skill**: setup-sdlc (pr-template sub-flow)
- **Step**: Step 7 — Validate
- **Operation**: `validate({ action: "pr_template" })`
- **Error**: full tool error message
- **Suggested investigation**: check `.sdlc/pr-template.md` file permissions; verify the plugin's MCP server is running

---

## Gotchas

1. **Existing template detection false negative.**
   *Symptom:* The sub-flow proposes creating a new PR template even though the project already has one, causing conflicts or duplicates.
   *Root cause:* The parent scans for `.github/PULL_REQUEST_TEMPLATE.md` but projects may store templates at `.github/PULL_REQUEST_TEMPLATE/*.md` (multi-template setup). The single-file check misses the directory-based pattern entirely.
   *Mitigation:* Parent should also check for `.github/PULL_REQUEST_TEMPLATE/` as a directory. If multiple templates exist there, the parent presents the list to the user and asks which one to use as a baseline.

2. **JIRA project key extraction fails silently.**
   *Symptom:* The generated template omits the Jira link section or formats it incorrectly (e.g., wrong project key prefix).
   *Root cause:* The `[A-Z]{2,10}-\d+` regex scans recent PR titles/bodies and branch names, but fails when the project uses a non-standard key format, when no PRs mention JIRA, or when the branch naming convention does not include the ticket key.
   *Mitigation:* If JIRA evidence is ambiguous (fewer than 2 matches, or multiple distinct key prefixes found), the parent asks the user for the project key explicitly before invoking this sub-flow.

3. **Interactive customization loop accumulates inconsistent state.**
   *Symptom:* After several edit rounds, the template contains contradictory sections (e.g., the user removed "Summary", then asked to "add linked issues above the summary" — the sub-flow re-adds a summary section implicitly).
   *Root cause:* Each edit is applied as a delta to the previous version. Contradictory edits are not detected because the sub-flow only tracks the latest change, not the full edit history.
   *Mitigation:* After each edit, re-present the full rendered template (not just the diff). Before applying an edit that references a previously removed section, confirm with the user whether the section should be restored.

---

## DO NOT

- Do NOT overwrite an existing `.sdlc/pr-template.md` without first showing the user the current template content and obtaining explicit "yes" consent.
- Do NOT skip the `validate({ action: "pr_template" })` step — an invalid template will break `gh pr create` for the entire project.
- Do NOT hard-code JIRA project keys or ticket formats based on assumptions — always derive from actual evidence or ask the user.
- Do NOT present a generic template without the parent's scan results — every template must be tailored to the project's conventions.
- Do NOT allow the edit loop to bypass validation — each final save must run the validator before marking the sub-flow complete.

---

## See Also

- `setup-sdlc --pr-template` — parent skill that invokes this sub-flow
- `setup-sdlc --dimensions` — sibling sub-flow for review dimensions
- [`/pr-sdlc`](../pr-sdlc/SKILL.md) — uses the template written by this sub-flow
