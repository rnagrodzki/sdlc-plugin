---
name: code-quality-review
description: General Go code quality — idiomatic style, error handling shape, naming, dead code, and package boundaries across the sdlc-plugin module.
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
  than no migration at all.
