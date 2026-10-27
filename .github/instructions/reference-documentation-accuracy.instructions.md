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
- A reference doc that says a write upserts or replaces a row names the key fields the code compares and the result when no row or several rows match; every statement of the key names the same fields.
- A reference doc that states the shape of a stored or returned value (JSON key set, a key shown as `null`, a nested object) matches the Go struct tags and the code that builds it; a key documented as `null` that the code leaves out is a defect.
- A call shown in a reference doc (function, argument order, result) matches the current signature and its call sites.
- A filename, id or key pattern lists every prefix or alternative that the code regex accepts, in the prose, the tables and the examples; a table row that names writers carries the id pattern too.
- A claim that names a count or list of cleanup, writer or caller paths names every call site that grep finds.
- A claim of "once", "only", "always" or "never" holds against every other rule and table row of the same doc.
