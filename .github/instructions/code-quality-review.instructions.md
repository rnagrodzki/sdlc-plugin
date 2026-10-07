---
applyTo: "**/*.go"
---
# code-quality-review — Review Instructions

General Go code quality — idiomatic style, error handling shape, naming, dead code, package boundaries, and documentation conventions across the sdlc-plugin module.

Default severity: medium

## Checklist

- Idiomatic Go: no unnecessary interfaces, no premature abstraction, no stuttering names
- Error handling: errors wrapped with context (`fmt.Errorf("...: %w", err)`), never silently discarded
- Doc comments: all exported identifiers have explicit `//` doc comments (not just `//go:embed` directives); file-header comment blocks separated from `package` by a blank line — verify with `grep -B1 '^package '` and confirm the line before `package` is blank, not a comment
- Internal packages: even though `internal/**` is unexported at module boundary, packages should have doc comments
- No dead code, no commented-out blocks, no debug `fmt.Println`/`log.Printf` outside intentional CLI output
- Function length and complexity reasonable; large handler files keep MCP tool logic separable
- Naming consistent with sibling files in same package
- Doc comments citing paths (`.sdlc`, `.sdlc-v2`) match `paths.DataDir`/`paths.LegacyDataDir` constants
- No exported identifiers with zero module-wide callers
- Nil-guarding on newly-pointer-typed struct fields before dereference
- Constant migrations: verify replacements are complete, no hardcoded literal divergence
- Testable design: functions that are tested and access live filesystem or git state should expose injectable parameters or dependencies (e.g. directory overrides, mocked implementations) so tests can avoid real I/O. Check existing test files in the change to verify whether tested functions expose such injection points; if not, flag as a testability blocker.
- Sibling polling/retry loops and error-classification logic: when a file contains two or more near-identical code paths, verify they agree on probe-vs-timeout ordering, error classification, and final-probe behavior; if one is fixed, the fix must be mirrored to the other or the paths consolidated
- Doc comments must match the code: if a comment claims a function does X when it actually does Y, or describes four cases when only two execute, flag it as a finding. A doc string is not just a presence requirement — it must be accurate.
- Doc comments stating a template or schema default value must be checked against the actual template/schema file, not just the code body or an existing test. A stale default description is a finding even when a test already covers the real behavior.
- Error return types use the `error` interface (with `errors.As` for value extraction), never a concrete pointer type like `*MovedKeysErr`, to avoid the nil-pointer-in-interface footgun where a `nil` concrete value does not compare equal to `nil` in an interface.
- MCP tool handlers: when a handler gains a new filesystem side effect (write, delete) or reads a new config file/section, re-audit the handler's registered tool description (passed to RegisterTools) for adjectives like 'stateless', 'read-only', or 'no side effects' — revise any now false, and re-verify all `*Out` field `jsonschema_description` tags and source-label enums remain accurate against the new behavior.
- Planning artifacts in comments: doc comments and inline comments must not reference planning-artifact identifiers (step numbers, Key Decision ids like KD15, plan section names like "Final Shape" or "(Deviations)", task IDs). Grep modified .go files with `grep -nE 'KD[0-9]+|\(Deviations\)|Final Shape|// Step [0-9]'` and flag any matches for removal.
- User-facing output: rendered text in reports, warnings, Next hints uses sentence case (first letter capitalized); contrast with Go errors which stay lowercase per convention
- Convergence/loop-termination checks: avoid fragile exact-equality (e.g. `round == maxRounds`); use `>=` or a monotone predicate/tolerance band so overshoot does not skip the exit condition
- Redundant computation: when a function is called multiple times with the same inputs in one scope (e.g. `LimitsFor` in a loop with an invariant argument), consolidate to a single call and reuse the result
