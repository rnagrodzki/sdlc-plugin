---
name: skill-cross-reference-consistency
description: When a skill, MCP tool, or sub-flow changes, verify every other skill/doc referencing it (See Also links, delegatedTo targets, pass-through flags, tool descriptions) stays accurate and non-contradictory.
triggers:
  - "plugins/sdlc/skills/**"
  - "internal/tools/**"
  - "internal/mcpserver/**"
  - "plugins/sdlc/agents/**"
severity: high
---

## What to check

- **See Also / delegated references.** A path or `delegatedTo` id another
  file points to must still exist, and its behavior/arguments must still
  match what the referencing text claims.
- **Pass-through flags.** If skill A's text says "pass through `--add` if
  present" to skill B, verify skill B's own Arguments table actually
  documents `--add`.
- **Same-tool cross-skill claims.** When two or more skills describe the
  same MCP tool's behavior, fields, or side effects, the descriptions
  must agree with each other AND with the tool's current Go source — not
  just be individually plausible.
- **Shared config keys.** When multiple SKILL.md files read or write the
  same config path (e.g. `plan.guardrails`, `pr.labels`), every
  describing skill's claim about that path's shape or ownership must be
  consistent with the others — no skill assuming a shape another skill's
  write contradicts.
- **Rename/removal completeness.** Before treating a skill or tool rename
  as complete, grep the whole `plugins/sdlc/skills/` tree for every prior
  name. A stale reference in an unrelated skill file is a contradiction,
  not a leftover formatting issue.
- **Command/MCP surface parity.** When a skill's prose claims a step
  "runs via `<tool>_<action>`" or lists an MCP tool as covering a step,
  verify that tool is actually registered (`internal/skillcheck`
  registries) and its action/behavior matches the claim — do not accept
  a plausible-sounding tool name without checking it exists.
- **Callee output consumed by the caller.** When a skill dispatches a
  sub-skill, a background worker, or an MCP tool whose returned fields
  carry a signal the caller must act on (an unstopped-worker report, a
  stop-failure list, a blocking verdict), the caller must name the step
  that reads each such field and state its action: stop, retry, or
  disclose to the user. A returned signal with no consuming step in the
  caller is a finding.

## What NOT to flag

- A skill intentionally overriding or extending another's default
  behavior via a documented flag — that is not a contradiction, only an
  unsynced default.
- Differences in wording or detail level that don't change the
  substantive claim (e.g. one skill briefly mentions a tool, another
  documents it fully) — flag only actual factual contradictions, not
  verbosity differences.
- Link reachability (HTTP/GitHub/Jira URLs) — that is `links_validate`'s
  job, not this dimension's.

## Cross-references

- `skill-wiring-consistency.md` — single-skill-to-tool parameter wiring
  within one dispatch.
- `skill-prose-accuracy.md` — a single skill's prose vs its own
  implementation.
- `skill-flow-soundness.md` — a single skill's internal flow shape.
- This dimension is the only one checking skill-to-skill and
  skill-to-tool consistency across the whole tree, not within one file.
