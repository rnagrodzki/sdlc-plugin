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
- Test fixtures that hardcode system state (e.g. Register*Tools call lists
  in skillcheck helpers) must be maintained in a single location, not
  duplicated across multiple test files. When system state changes, all
  dependent fixtures must be updated consistently.
- When a struct's field type changes (int -> *int) or a JSON tag changes
  (removing omitempty), identify and update every existing test in the same
  package that asserts the old serialization or default-value behavior.
  Existing tests not listed in the plan's file scope represent missed
  change scope.
- When a task modifies plugins/sdlc/skills/*/SKILL.md to add or change an
  MCP tool dispatch, verify every corresponding Register*Tools call is
  present in the matching internal/skillcheck/skillcheck_*_test.go
  registry -- grep all Register*Tools patterns before finalizing the
  task's file list. Plan Verification for such tasks must include
  `go test ./internal/skillcheck/...` (or full `go test ./...`), not just
  the modified package's unit tests.
- JS/Node test files (`*.test.js`, run via `node --test`) must be wired
  into CI (`.github/workflows/*.yml`), not just present locally — verify
  against the actual workflow file, not assumed.
- A test must actually exercise the code path it claims to cover; an empty
  body or an assertion the code never reaches does not count as coverage —
  verify by reading the test body, not by the presence of a `_test.go` or
  `.test.js` file alone.
- A passing read-only preflight check does not prove a subsequent write
  operation will succeed (permission requirements can differ). Tests that
  validate preflight behavior must be distinguished from tests that
  validate the write operation itself.
- When code dispatches over a static table or enum (e.g. four moved keys:
  pr.expectedAccount, execute.auto, execute.quality,
  execute.highRiskAutoApprove), tests must exercise every entry. Count the
  table entries and verify the test case count matches; a gap means
  unreachable code under normal operation.
- Multi-step file writes (e.g. atomic writes to two files in sequence) must
  have each failure point tested: first write succeeds/second fails, first
  write fails, and both succeed. Assert the on-disk state after each
  failure to verify partial writes do not corrupt state.
- When a refactor removes or widens a guard (e.g. drops a `method ==`
  check so more inputs reach a rewrite or splice), every newly admitted
  input combination needs its own test case. Removing a guard is the same
  as adding a branch: apply the same coverage bar.
- When a function has two or more discriminating inputs (e.g. a bool
  parameter combined with an enum, intent value, or configuration flag),
  every reachable combination's output must be asserted directly in a test
  case. A test exercising only one leg of a bool parameter or one value of
  an enum/intent discriminator is partial coverage; verify each combination
  named by the function's branch structure is invoked and its output state
  asserted.
- Every rejection branch of an input check needs its own test: a test for
  a nil `wave` does not cover the separate `wave < 1` branch. For an
  error-classification switch (e.g. a helper mapping sentinel errors to
  DomainError/InfraError), count its cases and verify one test case per
  case, asserting the error class, Msg and Suggestion, not just the Go type
  of one branch. An error branch that needs an unusual external condition
  (e.g. a git command exiting non-zero without leaving state behind) still
  needs a test when a PATH stub can reach it.
- When a task adds new config keys (e.g. `[execute] baseSync`,
  `[git] baseBranch`), each key must be named literally in a test case or
  assertion, not only exercised by a generic map-based walker, so a reviewer
  can see its coverage without reverse-engineering the test's iteration
  logic.
