---
name: skill-wiring-consistency
description: Validates that SKILL.md dispatch args match actual MCP tool *In struct JSON tags — catches silent parameter drops from name mismatches.
triggers:
  - "plugins/sdlc/skills/**"
  - "internal/tools/**"
severity: high
---

Review changes to skill SKILL.md files and tool `*In` structs for parameter
wiring consistency. When a skill dispatches an MCP tool, every parameter
name in the dispatch args must match a `json:"..."` tag on the tool's `*In`
struct. A mismatch means the value is silently dropped — the tool never
receives it.

## What to check

### Parameter name alignment

When a SKILL.md contains a tool dispatch instruction like:
```
Call pr_apply with title, body, releaseLevel, skipReleaseCheck: true
```

Verify each parameter name (`title`, `body`, `releaseLevel`, `skipReleaseCheck`)
exists as a `json:"..."` tag value on `PRApplyIn`. Flag any name that does
not match.

Common mismatch patterns:
- camelCase in skill vs snake_case in json tag (or vice versa)
- Renamed field in Go struct but stale name in SKILL.md
- New field added to struct but not wired in skill dispatch
- Typo in parameter name

### Conditional dispatch completeness

When a skill conditionally includes parameters (e.g., "when version ran,
include releaseLevel"), verify that ALL required conditional parameters are
included together. Example: `releaseLevel` requires `releaseSource` — if
the skill forwards one but not the other, flag it.

### Output field references

When a skill reads a tool's output (e.g., "if pr_prepare returns
accountMismatch: true"), verify the field name matches the tool's `*Out`
struct's `json:"..."` tag.

## What NOT to flag

- Parameter values (this dimension checks names, not values)
- Skill prose that mentions a parameter name in explanatory text rather
  than in a dispatch instruction
- Parameters documented with `omitempty` that are intentionally absent in
  a specific dispatch path

## Cross-references

- `mcp-tool-review.md` covers general tool quality.
- `mcp-contract-compliance.md` covers struct-level tag requirements.
- Guardrail: `skill-tool-param-sync` (execute) enforces this at execution time.
