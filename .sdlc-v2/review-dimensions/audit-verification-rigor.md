---
name: audit-verification-rigor
description: Audit and negative-assertion tasks must verify absence claims with explicit source-reading (grep, code inspection).
triggers:
  - ".sdlc-v2/**/*.md"
  - "plugins/sdlc/skills/**/*.md"
severity: high
---

## Checklist
- Claims of absence verified with grep/source inspection
- Verification method documented in task/review notes
- Multi-source claims enumerate specific files with grep results
- "New findings" distinguished from previously-documented ones
