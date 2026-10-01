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
- Every flag declared in a SKILL.md's front-matter `argument-hint` section (or parsed in Step 0) has a corresponding row or section in the matching `docs/skills/<skill>.md` file (e.g. in a Flags table, Arguments section, or command-line reference). A flag documented in SKILL.md but missing from docs is a finding.
- When a config-key migration or namespace rename affects multiple sections of a `docs/skills/*.md` file (e.g. Configuration section, Tips bullets, Flags table), verify **all** sections via grep for old key names and flag references, not just the obviously-related primary section. A partial sync creates contradictions within the file's own sections.
- When a SKILL.md introduces or modifies a blocking gate (e.g. a verdict classification that halts execution, prevents decomposition, or requires manual override), the corresponding `docs/skills/<skill>.md` must document: (1) what the gate blocks, (2) what conditions trigger it, (3) whether a bypass flag or option exists (`--auto`, `--force`) and what it does, or explicitly state that no bypass exists. The absence of a bypass is a fact to document, not an omission.
- When a SKILL.md change applies the same behavior (fail-fast condition, skip condition, blocking gate) to two or more steps, enumerate every step it touches by grepping SKILL.md, and confirm `docs/skills/<skill>.md` documents that behavior for each step. A documentation update covering only one sibling (e.g. documenting `verify-openspec` fail-fast-on-missing-disk but not `archive-openspec`) is a finding.

## What NOT to flag

- `docs/skills/*.md` content that is intentionally more general or example-driven than SKILL.md's terse procedural prose.
- Internal SKILL.md implementation detail (state file paths, internal helper names) with no user-facing doc obligation.

## Cross-references

- `skill-prose-accuracy.md` covers SKILL.md-vs-source-code drift (implementation, not docs/).
- `skill-wiring-consistency.md` covers SKILL.md dispatch args vs MCP tool struct tags.
- This dimension is the missing third leg: `docs/skills/*.md` vs SKILL.md itself.
