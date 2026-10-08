---
applyTo: "plugins/sdlc/skills/**/SKILL.md"
---
# skill-prose-accuracy — Review Instructions

Validates that SKILL.md prose descriptions accurately reflect actual implementation via direct grep/read, not inferred from build/test success alone.

## Verification Checklist
- Cross-section consistency: If a behavior or routing rule is described in multiple sections, verify all restatements match the current implementation. A header, Steps overview, or numbered step list is a restatement: each entry must match its body step's number, name, and action.
- Scaffold/resource preconditions: When prose claims a dispatch creates or seeds a resource, grep the handler code to confirm the resource is actually initialized.
- Schema enum validation: When prose references enum values, verify each is in the actual JSON schema definition.
- Tool output field enumeration accuracy: When a SKILL.md section enumerates the fields of an MCP tool's output (a manifest, a report structure), compare the listed fields against the tool's `*Out` struct in both directions. Every prose-listed field must exist in the struct, and every struct field documented as part of that tool's contract must be mentioned in the prose.
- Hardcoded section/structure lists: When SKILL.md enumerates or counts config sections, state-computation rule sections, enum tag values, const blocks, config-source or precedence chains, or other Go-defined structural sets, verify against the canonical source (e.g., internal/setupmeta.Sections()) that all elements are named, the count matches the elements listed, and the list stays synchronized with post-refactoring changes.
- Removed instructions audit: When a modification removes a paragraph that carried reader instructions (e.g., a field-collection step in setup), verify the instruction survives elsewhere or is ruled out in Key Decisions; silent deletion of an instruction is a finding.
- Field paths and ID format consistency: When prose reads fields from MCP result objects or defines ID templates for dispatch prompts (e.g. `writers.missingWriters`, `F-{dimension.name}-n` vs `F-{DIM_SLUG}-n`, the `ref` field on inline-exploration items), verify the field nesting matches the handler's `*Out` struct and the ID format matches what the coordination block or template actually uses. A mismatch breaks downstream processing.
- Absence and exclusion claims verification: When SKILL.md claims something is absent ("Port Notes are not recorded", "this field is not included"), grep the implementation to confirm the claim is accurate. Prose that contradicts implementation regarding presence/absence is a finding.
- CI payload prose: when a SKILL.md summarizes a .cjs payload script's branch logic (for example the promote level check), the prose must name every branch outcome the script produces and place each message where the script prints it.

Default severity: high
