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
