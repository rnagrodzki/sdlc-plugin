---
name: code-quality-review
description: General Go code quality — idiomatic style, error handling shape, naming, dead code, package boundaries, and doc comment conventions across the sdlc-plugin module.
triggers:
  - "**/*.go"
skip-when:
  - "**/*_test.go"
  - "**/testdata/**"
severity: medium
---

Review Go source changes for baseline code quality in this module
(`github.com/rnagrodzki/sdlc-plugin`):

- Idiomatic Go: no unnecessary interfaces, no premature abstraction, no
  stuttering names (`tools.ToolsX`), receivers named consistently per type.
- Errors are wrapped with context (`fmt.Errorf("...: %w", err)`) and never
  silently discarded (`_ = err` requires a comment justifying it).
- Exported identifiers in `internal/**` are still worth doc comments even
  though the package is unexported at the module boundary — this repo's
  existing packages (`internal/execx`, `internal/dimensions`,
  `internal/paths`) consistently lead with a package doc comment; new
  packages should follow the same convention.
- No dead code, no commented-out blocks left behind, no debug
  `fmt.Println`/`log.Printf` calls outside of intentional CLI output paths.
- Function length and cyclomatic complexity are reasonable; large
  `internal/tools/*.go` handler files should still keep each MCP tool's
  logic separable and testable.
- Naming is consistent with sibling files in the same package — check for
  drift before introducing a second convention.
- Documentation strings/doc comments citing paths (`.sdlc`, `.sdlc-v2`) or
  filenames must stay synchronized with the actual
  `paths.DataDir`/`paths.LegacyDataDir` constants and file-loading code.
- No exported identifiers with zero callers module-wide (verified via
  `grep -r`), no commented-out blocks, no debug `fmt.Println`/`log.Printf`
  outside intentional CLI output paths. Exported identifiers that compile
  but have no callers are still dead code.
- When a struct field's type changes from non-pointer to pointer (`*int`,
  `*string`, `*bool`), every dereference site must be nil-guarded before
  use or use a local-default pattern (e.g. `value := 0; if ptr != nil {
  value = *ptr }`). An unguarded dereference of a newly-pointer field
  causes a nil-pointer panic for callers omitting that field.
- When a task introduces a new constant (e.g. `paths.RunsSubdir`) to
  replace hardcoded string literals (e.g. `"execution"`), code review must
  verify the replacement is complete across the codebase — a partial
  migration creates two silently-diverging sources of truth and is worse
  than no migration at all. The same applies to deleting an exported
  symbol entirely — grep the whole repo, not just the changed package,
  since callers in unrelated packages are easy to miss.
- CI workflow files (`.github/workflows/*.yml`) that reference schema/config
  field names or paths must stay synchronized when those fields change —
  treat them like any other doc-comment path reference.
- An MCP tool annotation (e.g. read-only, idempotent) with a reason string
  must be checked against an actual call-graph/reference search of the
  handler — a plausible-sounding reason is not evidence the annotation is
  correct.
- Exported identifiers (`var`, `const`, `func`, `type`) must have an
  explicit `//` doc comment on the line immediately preceding them; a
  `//go:embed` directive alone is not sufficient — it is a compiler
  directive, not documentation. File-header comment blocks must be followed
  by a blank line before the `package` declaration, or they will be
  misattached as the package doc string rather than remaining as
  file-level comments.
- Testable design: functions that are tested and access live filesystem,
  git state, or external services should expose injectable parameters or
  dependencies (e.g. directory overrides, mocked implementations) so tests
  can use mock/temporary implementations instead of real I/O. A function
  without such an injection point forces its tests to perform real I/O,
  violating the `no-real-fs-git-in-tests` guardrail.
- Doc comments must be verified against the code body they describe; a doc
  string claiming a function does X when it does Y, or describing four
  cases when only two execute, is a finding. Exported identifiers with zero
  module-wide callers are dead code, regardless of presence of a doc
  comment.
- Error types must use the `error` interface, never a concrete pointer type
  (`*MovedKeysErr`). Callers use `errors.As` to extract values. The
  nil-pointer-in-interface footgun occurs when a concrete type `nil` is
  stored in an `error` interface and later compared to `nil` — it does not
  equal `nil`. Use `error` at function boundaries.
- Inline and doc comments must not carry references to planning artifacts:
  step numbers (`// Step 10.`), Key Decision ids (`KD9`, or parenthetical
  citations like `(KD15)`), task IDs, or plan section names (`"Final Shape"`,
  `(Deviations)`). These are authoring leftovers introduced during initial
  development and must be cleaned before shipping. The plan file is not
  committed with the code, so these references mean nothing to a later
  reader. Run `grep -nE '\bKD[0-9]+\b|\((Deviations|Final Shape)\)|"Final Shape"|// Step [0-9]'`
  on modified `.go` files to catch these patterns.
- Records passed as parameters or returned should use named structs, not
  positional tuples via arrays (`[2]string`). Positional syntax is fragile
  and does not scale when the record gains a fifth field.
- Doc comments must be complete, not only accurate: when the code handles a
  case the doc comment does not mention (a second action that returns the
  same type with extra status values, a field populated on more paths than
  stated, or an additional DomainError failure mode), that omission is a
  finding, the same as a claim the code does not back. Check MCP tool
  description strings the same way.
- Functions or types with near-identical names (differing only by a package
  prefix or a single token, e.g. `openspec.StagedChangeFromPlan` vs a local
  `openspecChangeFromPlan`) that extract different things from the same
  input are a finding: rename one so the name states what it reads, or
  distinguish them clearly in each doc comment.
- User-facing output text (warning messages in reports, Markdown Next hints,
  rendered logs) must use sentence case — capitalize the first letter. Do not
  confuse this with Go error values in the `error` interface, which follow Go
  convention and stay lowercase (`errors.New("file not found")`). Exported
  string constants representing user-visible text must start with a capital
  letter.
- Validator functions must not duplicate input checks. When a handler
  validates the same input condition in more than one place (e.g. two
  separate "file is required" checks for the same action), consolidate into
  a single validation point and call it from both paths. Duplicate checks
  diverge when one is updated and the other is not, and they hide the real
  validation logic under repeated boilerplate.
- Convergence checks and loop-termination conditions must avoid fragile
  exact-equality comparisons. For example, `round == maxRounds` fails when
  round is incremented past maxRounds; use `>=` instead. A convergence check
  like `score == prevScore` fails when score can skip the equality point;
  prefer monotone predicates or tolerance bands. Equality-based termination
  in critical control flow is brittle and should be replaced with robust
  comparison patterns.
- Redundant recomputation of values that are already available must be
  consolidated. When a function is called multiple times with the same
  inputs in the same scope (e.g. `LimitsFor` called in a loop with an
  invariant argument, or a cached value computed twice in different
  branches), consolidate to a single call and reuse the result. Redundant
  calls mask expensive operations and are worse than not optimizing at all.
