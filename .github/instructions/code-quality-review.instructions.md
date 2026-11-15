---
applyTo: "**/*.go"
---
# code-quality-review — Review Instructions

General Go code quality — idiomatic style, error handling shape, naming, dead code, package boundaries, and doc comment conventions across the sdlc-plugin module.

Default severity: medium

## Checks

- An MCP tool annotation (read-only, idempotent, open-world) covers every action of the tool. Search the call graph of each action, and of each action the change adds or changes. List each file write, rename or delete, and each call that leaves the process (a git push, a `gh` call, a network request). When any action makes such a call, the open-world hint must be true. A wrong annotation is a finding unless docs/internal/mcp-tool-annotations.md names an exception.
- A conversion or parse call (`strconv.Atoi`, `strconv.ParseInt`, `time.Parse`) whose error goes to `_` needs a comment on the line that states why the zero value is safe. Without it, a value that did not parse reads as a real zero in a count or a verdict.
- A parser of text that the code did not write (a review overview, a Markdown file) returns an error, or counts and warns, for a row it cannot read. It does not drop the row with no signal. Its tests cover an indented row, CRLF line ends, a block that never closes and a number that does not parse.
- A helper that two or more handlers, tools or routes call builds error and warning text with no tool, action or route name of its own. Each caller passes its name or adds its own prefix, and has a test that asserts the whole text. A reader that copies another reader and changes only a size cap, an error prefix or a type is a finding: give the existing reader a parameter.

## Note

In Claude Code reviews, files matching these patterns are excluded: **/*_test.go, **/testdata/**.
Copilot path-specific instructions do not support exclusion patterns — use judgment when findings apply to these files.
