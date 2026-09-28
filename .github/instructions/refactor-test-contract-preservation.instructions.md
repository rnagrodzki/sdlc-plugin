---
applyTo: "internal/**/*.go"
---
# refactor-test-contract-preservation — Review Instructions

Refactors that consolidate duplicate logic must preserve pre-refactor test-observable behavior — verify affected tests still pass against the consolidated path, not just tests for the new shared path.

Default severity: high

## Checklist

- When code paths are consolidated or a guard is removed, check that every affected test still asserts the same observable behavior. Do not rely on test names alone.
- If the diff weakens a test assertion (e.g. a specific content check replaced by a bare non-empty check) without a matching intended code change, flag it as a possible masked regression and ask for justification.
- Treat a parameter or assertion kept only "for signature stability" or "to keep tests green" as a warning sign of a hidden regression.
