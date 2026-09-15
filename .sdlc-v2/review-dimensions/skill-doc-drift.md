---
name: skill-doc-drift
description: Detect drift between docs/skills/*.md and SKILL.md source of truth
triggers:
  - "docs/skills/**"
  - "plugins/sdlc/skills/**/*.md"
severity: medium
---

## Checklist

- Every step, tool call, and flag documented in `docs/skills/<skill>.md` matches the corresponding `plugins/sdlc/skills/<skill>/SKILL.md` — SKILL.md is the source of truth.
- A step renamed, reordered, or removed in SKILL.md is reflected in the matching `docs/skills/*.md` file (or flagged if not).
- A tool name referenced in `docs/skills/*.md` still exists and is still called at the point in SKILL.md the doc describes.
- A field name or flag documented in `docs/skills/*.md` still matches SKILL.md's current dispatch args/output field names.
- New mandatory steps added to SKILL.md (e.g. a new "Step 1: Load State" gate) are not missing from the corresponding `docs/skills/*.md` file.

## What NOT to flag

- `docs/skills/*.md` content that is intentionally more general or example-driven than SKILL.md's terse procedural prose.
- Internal SKILL.md implementation detail (state file paths, internal helper names) with no user-facing doc obligation.

## Cross-references

- `skill-prose-accuracy.md` covers SKILL.md-vs-source-code drift (implementation, not docs/).
- `skill-wiring-consistency.md` covers SKILL.md dispatch args vs MCP tool struct tags.
- This dimension is the missing third leg: `docs/skills/*.md` vs SKILL.md itself.
