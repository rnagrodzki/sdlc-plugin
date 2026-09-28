---
applyTo: "docs/skills/**,plugins/sdlc/skills/**/*.md"
---
# skill-doc-drift — Review Instructions

Detect drift between docs/skills/*.md and SKILL.md source of truth

Default severity: medium

## Checklist

- Every step, tool call, and flag documented in `docs/skills/<skill>.md` matches the corresponding `plugins/sdlc/skills/<skill>/SKILL.md` — SKILL.md is the source of truth.
- A step renamed, reordered, or removed in SKILL.md is reflected in the matching `docs/skills/*.md` file (or flagged if not).
- A tool name referenced in `docs/skills/*.md` still exists and is still called at the point in SKILL.md the doc describes.
- A field name or flag documented in `docs/skills/*.md` still matches SKILL.md's current dispatch args/output field names.
- New mandatory steps added to SKILL.md (e.g. a new "Step 1: Load State" gate) are not missing from the corresponding `docs/skills/*.md` file.
- Every flag declared in a SKILL.md's front-matter `argument-hint` section (or parsed in Step 0) has a corresponding row or section in the matching `docs/skills/<skill>.md` file (e.g. in a Flags table, Arguments section, or command-line reference). A flag documented in SKILL.md but missing from docs is a finding.
- When a config-key migration or namespace rename affects multiple sections of a `docs/skills/*.md` file (e.g. Configuration section, Tips bullets, Flags table), verify **all** sections via grep for old key names and flag references, not just the obviously-related primary section. A partial sync creates contradictions within the file's own sections.
