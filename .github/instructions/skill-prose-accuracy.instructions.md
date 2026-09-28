---
applyTo: "plugins/sdlc/skills/**/SKILL.md"
---
# skill-prose-accuracy — Review Instructions

Validates that SKILL.md prose descriptions accurately reflect actual implementation via direct grep/read, not inferred from build/test success alone.

## Verification Checklist
- Cross-section consistency: If a behavior or routing rule is described in multiple sections, verify all restatements match the current implementation.
- Scaffold/resource preconditions: When prose claims a dispatch creates or seeds a resource, grep the handler code to confirm the resource is actually initialized.
- Schema enum validation: When prose references enum values, verify each is in the actual JSON schema definition.
- Hardcoded section/structure lists: When SKILL.md enumerates or counts config sections, state-computation rule sections, or other Go-defined structural sets, verify against the canonical source (e.g., internal/setupmeta.Sections()) that all elements are named, the count matches the elements listed, and the list stays synchronized with post-refactoring changes.
- Removed instructions audit: When a modification removes a paragraph that carried reader instructions (e.g., a field-collection step in setup), verify the instruction survives elsewhere or is ruled out in Key Decisions; silent deletion of an instruction is a finding.

Default severity: high
