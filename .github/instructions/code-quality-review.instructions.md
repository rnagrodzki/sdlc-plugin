---
applyTo: "**/*.go"
---
# code-quality-review — Review Instructions

General Go code quality — idiomatic style, error handling shape, naming, dead code, package boundaries, and documentation conventions across the sdlc-plugin module.

Default severity: medium

## Checklist

- Idiomatic Go: no unnecessary interfaces, no premature abstraction, no stuttering names
- Error handling: errors wrapped with context (`fmt.Errorf("...: %w", err)`), never silently discarded
- Doc comments: all exported identifiers have explicit `//` doc comments (not just `//go:embed` directives); file-header comment blocks separated from `package` by a blank line
- Internal packages: even though `internal/**` is unexported at module boundary, packages should have doc comments
- No dead code, no commented-out blocks, no debug `fmt.Println`/`log.Printf` outside intentional CLI output
- Function length and complexity reasonable; large handler files keep MCP tool logic separable
- Naming consistent with sibling files in same package
- Doc comments citing paths (`.sdlc`, `.sdlc-v2`) match `paths.DataDir`/`paths.LegacyDataDir` constants
- No exported identifiers with zero module-wide callers
- Nil-guarding on newly-pointer-typed struct fields before dereference
- Constant migrations: verify replacements are complete, no hardcoded literal divergence
