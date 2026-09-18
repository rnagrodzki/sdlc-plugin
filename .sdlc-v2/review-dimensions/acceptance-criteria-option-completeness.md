---
name: acceptance-criteria-option-completeness
description: Plan acceptance criteria offering multiple options/paths must have every option independently viable and testable, not a set where only one option is realistic.
triggers:
  - "*.md"
severity: medium
---

## Scope
- When a task's Acceptance Criteria lists multiple valid options (e.g.
  "either A or B"), verify each option is independently implementable and
  verifiable — not a decoy.
- Flag acceptance criteria where one option is clearly infeasible given
  the current codebase, making the stated choice illusory.
