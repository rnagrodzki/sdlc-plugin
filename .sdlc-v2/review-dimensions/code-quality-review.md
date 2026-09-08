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
