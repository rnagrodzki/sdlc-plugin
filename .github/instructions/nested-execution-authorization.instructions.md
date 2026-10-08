---
applyTo: "plugins/sdlc/skills/**/SKILL.md,internal/tools/execute_state.go"
---
# nested-execution-authorization — Review Instructions

When execute runs as a nested dispatch inside another skill, TaskStop/stall-handling authorization must be explicitly verified, not assumed to work identically to top-level execution.

- Block-cap mark tracking: when execute runs nested inside another skill and a dispatched task modifies the ship state, verify the reconcile step (if any) correctly preserves the block-cap exhaustion mark on the matching ship step. The reconcile must not erase side-effect marks without recording them in the output or the operator-visible history (e.g. the deferred ledger) — a reconcile that silently clears a nested-execute's block-cap mark leaves the operator without trace of why that execute halted.
- Any skill that dispatches background workers (review as well as execute) is in scope. Verify each started worker is ended on stall, error, interrupt, or cancel, and that a nested run did not skip the stall path after a TaskStop authorization error. Name each worker that could not be stopped in the output. The dispatching skill must read that list and act on each entry.

Default severity: high
