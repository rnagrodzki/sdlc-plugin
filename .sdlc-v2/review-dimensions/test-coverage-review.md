---
name: test-coverage-review
description: New or changed Go source must ship with corresponding _test.go coverage, per this project's own plan.guardrails policy.
triggers:
  - "internal/**/*.go"
  - "cmd/**/*.go"
severity: medium
---

This repository already has 56 `_test.go` files plus `tests/integration/`
and `tests/acceptance/` suites, and `.sdlc-v2/config.toml`'s own
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
- JS/Node test files (run via `node --test`) must be wired
  into CI (`.github/workflows/*.yml`), not just present locally — verify
  against the actual workflow file, not assumed.
- A test must actually exercise the code path it claims to cover; an empty
  body or an assertion the code never reaches does not count as coverage —
  verify by reading the test body, not by the presence of a `_test.go` or
  `.test.js` file alone. The claim includes the test name and comment: a
  name such as `LiveRunWinsOverKeptRun` needs a fixture in which the two
  sides differ and an assertion that fails when the other side wins. A name
  or comment that claims more than the assertions pin is a finding: narrow
  the name or add the assertion.
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
- Fallback branches that contain inner error guards (e.g., a file-write
  fallback that itself checks for read errors) must have test cases
  exercising both the primary failure (triggering the fallback) and the
  inner error condition. When the inner error cannot be produced via a real
  temp-dir filesystem (e.g., a permission error in the middle of reading),
  the code must expose an injectable seam; the test task must introduce
  that seam before the test task that exercises it (per
  no-real-fs-git-in-tests guardrail).
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
  asserted. The discriminating inputs may come from two producers, for
  example the status field a ship run writes and the status field an
  execute run writes, which one reader merges into one result. Then at
  least one test case sets both fields in the same fixture and asserts the
  merged result; tests that set one field at a time do not show how the two
  interact.
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
- Config-read error paths must be tested. When an MCP tool handler calls
  `config.Read`, `config.ReadSection`, or a similar config-loading function,
  the error path (parse failure, permission error, the `ErrNotFound`
  distinction) must have an explicit test case exercising it, not only the
  happy path where config is present. Tests must verify the error reaches
  the caller as a structured error (DomainError/InfraError with Suggestion)
  rather than being silently treated as "not configured".
- When a code path produces a warning or error message (warning fields in
  *Out structs, Msg, Suggestion, next fields in structured errors, or
  rendered log text), the test must assert the message text itself, not
  only that the error condition occurred. A test exercising an
  error-producing branch that does not verify the message text is
  incomplete coverage — the text may change and silent drift would occur.
  Exclude errors.New/fmt.Errorf return values (which follow Go convention
  and stay lowercase); focus on text that reaches the user or LLM renderer.
- When a guard or refusal condition evaluates a collection (sections,
  files, keys, rows in a table), test coverage must exercise both
  single-element and multi-element cases. A single-element test that passes
  does not prove the multi-element path works — guards that check
  boundaries or relationships between elements may fail only when two or
  more elements exist. Example: a batch write that refuses several
  sections must be tested with two refused sections, to confirm the
  recovery message names each one and not only the first.
- When a function instantiates a structured error (InfraError,
  DomainError, DataError) in a default or catch-all arm (e.g., after
  specific-condition checks fail), test the default arm directly: use an
  injectable seam to reach it if the failure is unproducible via the real
  filesystem, and assert the error class, Msg, and Suggestion.
- When a config key, schema field, or handler default carries a specific
  value (e.g. a cap, limit, or threshold), a test must assert the default
  value at the tool output boundary (manifest, `*Out` struct) for every
  consumer mode or downstream phase that reads it. A unit test of the
  resolver or factory function alone does not prove the default reaches a
  specific phase like `plan_critique`; the boundary test must verify
  end-to-end propagation from default value to the consumer.
- A negative or decoy test (an input that must be rejected, skipped or
  ignored) differs from a valid input only in the property under test and
  satisfies every other condition of the check, so that condition alone
  decides the outcome. Confirm the test fails when that condition is
  removed: a decoy that an earlier condition already rejects does not cover
  the later one. Example: a decoy name meant to fail a date check that also
  fails the suffix check.
- A limit, cap or maximum count is tested on both sides of its boundary: one
  case exactly at the limit (accepted) and one case at limit+1 (rejected,
  and the error text names the limit and the value). Derive both inputs
  from the limit constant or variable, not from a hand-typed number, so a
  changed limit cannot leave the test passing. When the real limit is too
  costly to reach, lower it through an injectable seam. A test far from the
  boundary (an empty input, or many times the limit) does not pin it.
- A test checks the error of every call it uses to inspect state (for
  example `os.ReadDir` or `os.ReadFile`). A discarded error makes a failed
  inspection read as an empty result, and the assertion passes for the
  wrong reason. When the code under test wraps a lower-level error (a
  typed error with a `Cause`, or `%w`), the test asserts the cause with
  `errors.Is` or `errors.As`, not only the message text.
- A failure test that needs an operating-system condition (for example a
  `chmod` that denies access) and skips when the process runs as root
  (`os.Geteuid() == 0`) gives no coverage in a root container, because it
  never runs there. Reach the same failure through an injectable seam or
  through a condition that root cannot bypass, such as a regular file where
  a directory is required. This extends the `t.Skip()` rule above.
- When a change adds a new accepted form of an input next to a form that
  stays supported (a new file-name scheme beside the legacy name, a new key
  beside the old key), each error-path and edge-case test of that input
  runs with both forms, or the test states why one form cannot reach the
  branch. Tests updated for the happy path only leave the failure branches
  of the new form unexercised.
- A write that updates an existing row or record (a status change, an upsert
  that matches an existing key) needs a test that seeds the row with a field
  the update does not supply, such as a linked ID, runs the update, and
  asserts the field is still there. A test that starts from an empty row, or
  that asserts only the field the update sets, passes when the update drops
  the other fields.
- A write that replaces a whole table or section (a guardrail table, a settings
  block) needs a test that seeds what the write does not name: a comment line,
  an entry that stays, and the id of each kept entry. The test runs the write
  and asserts that each seeded item is unchanged. A test that starts from an
  empty table passes when the write drops them. This extends the row-update
  bullet above from one row to a whole-table rewrite.
- Code that reads or writes a target chosen from more than one candidate (the
  main root or the active worktree, a user file or a project file) needs a test
  in which the candidates differ, such as two directories with different
  content. The test asserts that the chosen candidate is read or changed and
  that the other one is left alone. A test with one candidate, or with equal
  candidates, passes whichever one the code reads.
- A test set for a guard (a check that decides whether a write, a delete or a
  rewrite goes ahead) covers every key the guard protects, a key nested or
  dotted inside a guarded value, and the empty and wrong-type forms of each.
  When a parity test compares the guard with a reference decoder, it includes
  each shape the reference special-cases (an array-of-tables form, a missing
  key, a comment-only file). A parity test that skips one of those shapes does
  not show that the two agree. This extends the static-table bullet above to
  guards, nested forms and parity tests.
- Code that finds text in a stored file and reads or replaces it (the line
  under an anchor, a marker, a section) needs tests at the edges of what the
  search can reach: a match on the first line, a match on the last line, an
  end of file with no trailing newline, and a match exactly at the edge of a
  read or search window with one just past it. Each test asserts the full
  resulting text, not only that the anchor is still present. A test set that
  matches only in the middle of a well-formed file passes when the replace
  drops or doubles the last line.
- When an action can return a normal outcome and a warning in one result (an
  existing file that is reused and a warning about it), one test asserts both
  the outcome and the warning text, for every action that reaches the state.
  When two actions or tools share a resource, a state that a test pins for one
  action is pinned for the other, or the test states why the other action
  cannot reach it. A test that checks only the outcome, or only one of the two
  actions, passes when the other action drops the warning or reports another
  outcome.
