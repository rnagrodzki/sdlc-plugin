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
- A type assertion on a value read from a stored file or a state map uses the two-value form (`v, ok := x.(T)`) and handles `ok == false` with an error that names the file and the key. The same assertion on one key at two or more sites is one helper that every reader calls. An unchecked copy at any site is a finding.
- A comment on a struct field that has a `jsonschema_description` tag adds only what the tag cannot say, or is left out. A comment that repeats the tag text is a finding, because the two copies drift when one is edited.
- A constant that names one member of a family of literals of one kind (prefixes, markers, separators) has a constant for each sibling in the same block, or all members come from one helper. A one-member constant beside inline copies of the other members is a finding, because the copies drift when a prefix changes.

## Note

In Claude Code reviews, files matching these patterns are excluded: **/*_test.go, **/testdata/**.
Copilot path-specific instructions do not support exclusion patterns — use judgment when findings apply to these files.
