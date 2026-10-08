---
applyTo: "plugins/sdlc/skills/**/state-format.md,plugins/sdlc/skills/**/config-format.md,plugins/sdlc/skills/**/*reference.md"
---
# reference-documentation-accuracy — Review Instructions

Verifies that skill reference docs (state-format.md, config-format.md, *reference.md) match the Go constants, regexes, and call sites that define the behavior they describe.

Default severity: high

## Verification Checklist
- Config-section references in prose and examples stay in sync after restructuring (e.g. `[planStyle]` splitting into `[style]` + `[planStyle]`)
- Doc headlines, section anchors, and cross-references are verified to still exist after a doc restructuring
- Precedence or conflict claims between two keys or flags (for example `steps` wins over `quick`) match the validator branch that handles the pair; a pair the code rejects together is documented as rejected, not as a winner.
- Example blocks (config snippets, step lists, flag lines) follow the rules the same doc states and list exactly the canonical set in the Go source.
- Named git refs (default branch, base branch, target branch) match the ref the code reads for that step.
