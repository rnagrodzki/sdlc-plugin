---
name: test-coverage-review
description: New or changed Go source must ship with corresponding _test.go coverage, per this project's own plan.guardrails policy.
triggers:
  - "internal/**/*.go"
  - "cmd/**/*.go"
severity: medium
---

This repository already has 56 `_test.go` files plus `tests/integration/`
and `tests/acceptance/` suites, and `.sdlc-v2/config.json`'s own
`plan.guardrails` states explicitly: "Every task that creates or modifies
source code must include corresponding test cases" (severity: error). Hold
new changes to that same bar:

- A new or materially-changed exported function/method in `internal/**` has
  a corresponding test in the same package (or, for behavior spanning
  packages, in `tests/integration/` or `tests/acceptance/`).
- New MCP tool handlers in `internal/tools/*.go` are covered by both a
  narrow unit test and, where the tool has side effects on the filesystem
  or git state, an integration-tagged test (`-tags integration`, matching
  what `.github/workflows/test.yml` runs).
- Error paths (not just the happy path) are exercised — this project favors
  sentinel errors and `errors.Is`/`errors.As`; tests should assert against
  the sentinel, not just a non-nil error.
- Table-driven tests follow the style already established in sibling
  `_test.go` files in the same package rather than introducing a new
  pattern.
- No test is skipped or `t.Skip()`-guarded without a linked follow-up
  explaining why.
- Data-structure edge cases are tested, especially in output
  serialization: nil vs empty collections, zero counts, boundary
  conditions, and all possible output states from tool handlers (clean
  pass, empty results, error cases). Tests must verify that null never
  appears where an empty array/object is expected.
