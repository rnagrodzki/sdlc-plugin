---
name: skill-wiring-consistency
description: Validates that SKILL.md dispatch args match actual MCP tool *In struct JSON tags AND that every tool reference is registered in internal/skillcheck shared registries — catches silent parameter drops and missing tool registrations.
triggers:
  - "plugins/sdlc/skills/**"
  - "internal/tools/**"
  - "internal/skillcheck/**"
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

When a skill conditionally includes parameters, verify that ALL required
conditional parameters are included together. Example: `releaseLevel` requires
`releaseSource` — if the skill forwards one but not the other, flag it.

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

### Skillcheck registry coverage

When a SKILL.md file gains or modifies a tool dispatch, verify every
`internal/skillcheck/skillcheck_*_test.go` file that enumerates MCP tools
has a matching `Register*Tools()` call. Example: if `skills/execute/SKILL.md`
adds a call to `learnings_log`, then `internal/skillcheck/skillcheck_plan_test.go`'s
`planSkillsListTools` must call `tools.RegisterLearningsTools(srv)`. A
missing registration causes the skillcheck validator to reject the tool
reference with an "unknown tool" error, but only when skillcheck's own
tests run — not during the touched package's unit tests.

### Input structure and type validation

Verify parameter names, types, and structure in a SKILL.md's constructed
tool-call args match the tool's documented schema, including for
composite/nested types.

### Output verification before downstream dispatch

Verify a tool's output structure (e.g. presence of a `## Contract` section)
before downstream tasks consume it as input.

### Sub-skill approval-gate orchestration

When a SKILL.md dispatches another skill via the Agent tool, verify that
any approval gates in the dispatched skill are satisfied at the
orchestrator level, not assumed to work within the sub-agent. Agent-tool
sub-agents cannot call AskUserQuestion directly. If a dispatched skill has
a step calling AskUserQuestion, the orchestrator must explicitly plan to:
(1) receive the paused sub-agent's request for approval, (2) call
AskUserQuestion from the orchestrator context, (3) send the user's answer
back to the sub-agent via SendMessage. Flag any dispatch of a skill with
documented approval gates that lacks an explicit orchestration strategy.

### Dispatch args notation clarity

When a SKILL.md documents a tool dispatch with flag arguments, verify the
notation is unambiguous — show a concrete filled example or explicitly
document shorthand/interpolation, rather than bare flags a reader could
take literally.

## Cross-references

- `mcp-tool-review.md` covers general tool quality.
- `mcp-contract-compliance.md` covers struct-level tag requirements.
- Guardrail: `skill-tool-param-sync` (execute) enforces this at execution time.
