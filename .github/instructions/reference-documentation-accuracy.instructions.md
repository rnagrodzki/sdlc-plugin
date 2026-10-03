---
applyTo: "plugins/sdlc/skills/**/state-format.md,plugins/sdlc/skills/**/config-format.md,plugins/sdlc/skills/**/*reference.md"
---
# reference-documentation-accuracy — Review Instructions

Verifies that skill reference docs (state-format.md, config-format.md, *reference.md) match the Go constants, regexes, and call sites that define the behavior they describe.

Default severity: high

## Verification Checklist
- Config-section references in prose and examples stay in sync after restructuring (e.g. `[planStyle]` splitting into `[style]` + `[planStyle]`)
- Doc headlines, section anchors, and cross-references are verified to still exist after a doc restructuring
