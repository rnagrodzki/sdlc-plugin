---
applyTo: "plugins/sdlc/skills/**/SKILL.md"
---
# skill-prose-accuracy — Review Instructions

Validates that SKILL.md prose descriptions accurately reflect actual implementation via direct grep/read, not inferred from build/test success alone.

## Verification Checklist
- Cross-section consistency: If a behavior or routing rule is described in multiple sections, verify all restatements match the current implementation.
- Scaffold/resource preconditions: When prose claims a dispatch creates or seeds a resource, grep the handler code to confirm the resource is actually initialized.
- Schema enum validation: When prose references enum values, verify each is in the actual JSON schema definition.

Default severity: high
