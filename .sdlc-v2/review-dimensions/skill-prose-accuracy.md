---
name: skill-prose-accuracy
severity: high
description: Validates that SKILL.md prose descriptions accurately reflect actual implementation via direct grep/read, not inferred from build/test success alone.
triggers:
  - plugins/sdlc/skills/**/SKILL.md
---

## Rules
- When a SKILL.md describes a mechanism, verify accuracy by direct grep/read against actual source code.
- Prose drift between documentation and implementation is not caught by build/test success; explicit verification required.
- Field names cited in skill prose (e.g. task object fields for wave-start)
  must match the tool's input schema exactly — grep the struct, do not
  assume from a similar-sounding prior name (e.g. "title" vs "name").
- Ordering/sequencing claims (e.g. "GATES runs before wave-done") must be
  verified against the actual dispatch code, not assumed from the skill's
  own narrative structure.
- Task-ID format claims in skill prose must match the format actually
  validated in code.
- Verification-procedure prose (e.g. spec-compliance steps) must match the
  actual review-dimension/guardrail text it references or summarizes.
- Preflight/verification claims of the form "if X passes, Y will succeed"
  must be checked against real permission/behavior differences in the
  underlying command — a passing read-only check does not prove a write
  operation will succeed.
