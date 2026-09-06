# Plan Template Sub-Flow

Sub-flow of `/setup-sdlc --plan-template`. Scaffolds a project-owned `.sdlc/plan-template.md`
by copying the shipped default (`plan-sdlc/plan-template-default.md`). Once present, the
project copy becomes the active template: `plan-sdlc`'s Step 2 planner follows it when
writing plan sections, and PF10 reads it for required-section presence checks.

---

## Arguments

None — this sub-flow takes no arguments.

---

## Workflow

### Step 1 — Check for an Existing Template

Check whether `.sdlc/plan-template.md` already exists (Glob for `.sdlc/plan-template.md`).

**If it exists:** Read and show the current file content to the user, then use
AskUserQuestion:

> `.sdlc/plan-template.md` already exists. Replace it with the default template?

Options:
- **replace** — overwrite with the shipped default (proceed to Step 2)
- **cancel** — exit without changes

On **cancel**: print `No changes made — existing .sdlc/plan-template.md kept.` and stop.

**If it does not exist:** proceed directly to Step 2.

---

### Step 2 — Copy the Default Template

```bash
# Substitute <PLUGIN_ROOT> from the `sdlc plugin root:` context line.
mkdir -p .sdlc
cp "<PLUGIN_ROOT>/skills/plan-sdlc/plan-template-default.md" .sdlc/plan-template.md
```

Confirm the copy succeeded (file exists and is non-empty) before continuing.

---

### Step 3 — Print Summary

Read the written `.sdlc/plan-template.md` and print a summary of its defined sections:

```
Written to .sdlc/plan-template.md

Required Sections:
  <list each `## Required Sections` bullet from the file>

Discovery Questions:
  <list each `## Discovery Questions` bullet from the file>

Verification Patterns:
  <list each `## Verification Patterns` bullet from the file>

This template is now the active plan template. plan-sdlc's Step 2 planner follows it when
writing plan sections, and PF10 reads it for required-section presence checks under --final.

To customize: edit .sdlc/plan-template.md directly — add, remove, or reorder items under
each heading. To reset to the shipped default, re-run `/setup-sdlc --plan-template`.
```

---

## DO NOT

- Do NOT overwrite an existing `.sdlc/plan-template.md` without first showing its current
  content and obtaining explicit "replace" consent via AskUserQuestion.
- Do NOT edit `plan-sdlc/plan-template-default.md` itself — it is the shipped source; the
  project copy at `.sdlc/plan-template.md` is the customization point.
- Do NOT write the file with the Write or Edit tools — always copy via `cp` so the shipped
  default is reproduced byte-for-byte.

---

## See Also

- [`/plan-sdlc`](../plan-sdlc/SKILL.md) — consumes the active plan template when drafting plans
- [`plan-template-default.md`](../plan-sdlc/plan-template-default.md) — the shipped default this sub-flow copies
- [`/setup-sdlc --guardrails`](../setup-sdlc/SKILL.md) — sibling sub-flow for plan guardrails
