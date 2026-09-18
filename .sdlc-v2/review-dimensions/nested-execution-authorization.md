---
name: nested-execution-authorization
description: When execute runs as a nested dispatch inside another skill, TaskStop/stall-handling authorization must be explicitly verified, not assumed to work identically to top-level execution.
triggers:
  - "plugins/sdlc/skills/**/SKILL.md"
  - "internal/tools/execute_state.go"
severity: high
---

## Scope
- Nested execute dispatches (e.g. /ship invoking execute) may receive a
  hard TaskStop authorization error instead of the normal stall-handling
  flow.
- Verify the stall-handling path was actually exercised in a nested run,
  not silently bypassed.
- Skill documentation describing nested dispatch must state this
  authorization difference explicitly.
