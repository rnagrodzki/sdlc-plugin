# Plan Template Sub-Flow

Sub-flow of `/setup --plan-template`. Scaffolds a project-owned `.sdlc-v2/plan-template.md`
by copying the shipped default (`plan/plan-template-default.md`). Once present, the
project copy becomes the active template: `plan`'s Step 2 planner follows it when
writing plan sections, and PF10 reads it for required-section presence checks.

---

## Arguments

None — this sub-flow takes no arguments.

---

## Workflow

### Step 1 — Check for an Existing Template

Call `setup_init({ readPlanTemplate: true })` → `{ ok, exists, content }`.

**If `exists` is true:** show `content` to the user, then use AskUserQuestion:

> `.sdlc-v2/plan-template.md` already exists. Replace it with the default template?

Options:
- **replace** — overwrite with the shipped default (proceed to Step 2)
- **cancel** — exit without changes

On **cancel**: print `No changes made — existing .sdlc-v2/plan-template.md kept.` and stop.

**If `exists` is false:** proceed directly to Step 2.

---

### Step 2 — Copy the Default Template

```
setup_init({ writePlanTemplate: true })
```

The tool resolves the shipped default and copies it byte-for-byte to
`.sdlc-v2/plan-template.md` under the main worktree root. Confirm `ok: true` in the
response before continuing.

---

### Step 3 — Print Summary

Call `setup_init({ readPlanTemplate: true })` → `{ ok, exists, content }` and print a summary
of `content`'s defined sections:

```
Written to .sdlc-v2/plan-template.md

Required Sections:
  <list each `## Required Sections` bullet from the file>

Discovery Questions:
  <list each `## Discovery Questions` bullet from the file>

Verification Patterns:
  <list each `## Verification Patterns` bullet from the file>

This template is now the active plan template. plan's Step 2 planner follows it when
writing plan sections, and PF10 reads it for required-section presence checks under --final.

To customize: edit .sdlc-v2/plan-template.md directly — add, remove, or reorder items under
each heading. To reset to the shipped default, re-run `/setup --plan-template`.
```

---

## DO NOT

- Do NOT overwrite an existing `.sdlc-v2/plan-template.md` without first showing its current
  content and obtaining explicit "replace" consent via AskUserQuestion.
- Do NOT edit `plan/plan-template-default.md` itself — it is the shipped source; the
  project copy at `.sdlc-v2/plan-template.md` is the customization point.
- Do NOT write the file with the Write, Edit, or Bash `cp` — always call
  `setup_init({ writePlanTemplate: true })` so the shipped default is reproduced
  byte-for-byte, main-worktree-rooted, with no path returned to this skill.

---

## See Also

- [`/plan`](../plan/SKILL.md) — consumes the active plan template when drafting plans
- [`plan-template-default.md`](../plan/plan-template-default.md) — the shipped default this sub-flow copies
- [`/setup --guardrails`](../setup/SKILL.md) — sibling sub-flow for plan guardrails
