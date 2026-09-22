---
applyTo: "plugins/sdlc/skills/**/SKILL.md,internal/tools/execute_state.go"
---
# nested-execution-authorization — Review Instructions

When execute runs as a nested dispatch inside another skill, TaskStop/stall-handling authorization must be explicitly verified, not assumed to work identically to top-level execution.

- Block-cap mark tracking: when execute runs nested inside another skill and a dispatched task modifies the ship state, verify the reconcile step (if any) correctly preserves the block-cap exhaustion mark on the matching ship step. The reconcile must not erase side-effect marks without recording them in the output or the operator-visible history (e.g. the deferred ledger) — a reconcile that silently clears a nested-execute's block-cap mark leaves the operator without trace of why that execute halted.

Default severity: high
