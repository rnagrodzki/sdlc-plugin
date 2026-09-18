---
applyTo: "internal/**/*.go"
---
# refactor-test-contract-preservation — Review Instructions

Refactors that consolidate duplicate logic must preserve pre-refactor test-observable behavior — verify affected tests still pass against the consolidated path, not just tests for the new shared path.

Default severity: high
