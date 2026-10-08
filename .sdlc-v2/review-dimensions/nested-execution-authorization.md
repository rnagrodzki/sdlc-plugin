---
name: nested-execution-authorization
description: When any skill (including review) runs as a nested dispatch inside another skill, TaskStop/stall-handling authorization must be explicitly verified, and abandoned background workers must be explicitly cleaned up via TaskStop or equivalent termination.
triggers:
  - "plugins/sdlc/skills/**/SKILL.md"
  - "internal/tools/execute_state.go"
severity: high
---

## Scope
- Nested skill dispatches (e.g. /ship invoking execute or review) may receive
  a hard TaskStop authorization error instead of the normal stall-handling
  flow.
- Verify the stall-handling path was actually exercised in a nested run,
  not silently bypassed.
- Skill documentation describing nested dispatch must state this
  authorization difference explicitly.
- When a skill dispatches background workers or sub-agents that can be
  abandoned (e.g. when a stall occurs or an error forces early exit),
  verify TaskStop or equivalent cleanup is called in Step 3 stall handler,
  Step 9 cleanup, and all error-recovery paths. No worker should be left
  running unattended after dispatch failure or abandonment.
- Tell a missing worker (dispatched, but never reported in) from an
  abandoned worker (started, then left running after a stall or error).
  Verify the results step names each missing worker, so the user sees that
  an expected worker did not arrive. Verify the stall handler and the
  normal-completion cleanup step both end each abandoned worker, via
  TaskStop or equivalent.
- Verify every entry that dispatches the same workers carries the
  nested-dispatch note (for example the ship entry for review as well as
  the ship entry for execute). A note present in one entry and absent from
  its sibling is a finding.
