---
name: refactor-test-contract-preservation
description: Refactors that consolidate duplicate logic must preserve pre-refactor test-observable behavior — verify affected tests still pass against the consolidated path, not just tests for the new shared path.
triggers:
  - "internal/**/*.go"
severity: high
---

## Scope
- When a refactor merges two near-identical functions or code paths,
  identify every test that depended on a pre-refactor behavior difference
  (e.g. an unconditional write that the merged path now makes conditional).
- Run the full affected test suite, not just tests targeting the new
  shared path.
- Do not assume behavioral equivalence from the refactor's stated intent
  alone — verify it by running the tests.
