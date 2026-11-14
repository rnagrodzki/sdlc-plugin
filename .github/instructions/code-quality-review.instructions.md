---
applyTo: "**/*.go"
---
# code-quality-review — Review Instructions

General Go code quality — idiomatic style, error handling shape, naming, dead code, package boundaries, and doc comment conventions across the sdlc-plugin module.

Default severity: medium

## Checks

- An MCP tool annotation (read-only, idempotent, open-world) covers every action of the tool. Search the call graph of each action, and of each action the change adds or changes. List each file write, rename or delete, and each call that leaves the process (a git push, a `gh` call, a network request). When any action makes such a call, the open-world hint must be true. A wrong annotation is a finding unless docs/internal/mcp-tool-annotations.md names an exception.

## Note

In Claude Code reviews, files matching these patterns are excluded: **/*_test.go, **/testdata/**.
Copilot path-specific instructions do not support exclusion patterns — use judgment when findings apply to these files.
