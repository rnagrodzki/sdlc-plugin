---
name: reference-documentation-accuracy
description: Verifies that skill reference docs (state-format.md, config-format.md, *reference.md) match the Go constants, regexes, and call sites that define the behavior they describe.
triggers:
  - "plugins/sdlc/skills/**/state-format.md"
  - "plugins/sdlc/skills/**/config-format.md"
  - "plugins/sdlc/skills/**/*reference.md"
severity: high
---

## Rules

- When a document serves as a reference for implementation internals
  (state-format.md, plan-format-reference.md, config-format.md, or similar),
  verify every prose claim about constants, regexes, enums, marker lists,
  size limits, and cleanup paths against the Go source that defines them.
- Grep the implementation for the named constants (e.g. `requiredPlanMarkers`
  in `internal/hooks/stop_hooks.go`), the functions that perform the described
  operation (e.g. `execReapRunDirectories` in `internal/tools/execute_state.go`
  for TTL cleanup of `runs/`), and the validation regexes (e.g. `writerIDRe`
  in `internal/tools/plan.go`). Confirm each detail is present and matches
  exactly.
- Every field, marker, or key the doc refers to in prose must also appear in
  its own tables and example JSON. A table that lists fewer entries than the
  prose counts is a finding.
- Documented limits must include the id and name constraints that the
  companion SKILL.md relies on (length caps, allowed characters), not only
  payload size limits.
- Cleanup and persistence claims ("X is removed only by Y", "Z does not touch
  W") must list every call site that grep finds. A claim that names fewer
  cleanup paths than the source has is a finding.
- The reference doc and its companion SKILL.md must not contradict each
  other. When they disagree, the Go source decides which one is wrong.
- Do not assume correctness because the doc ships clean or tests pass. Tests
  do not read prose; cross-check prose against source directly.
- Example mismatch: plan `state-format.md` said `gcStateFiles` does not touch
  evidence directories and named two cleanup paths (the Stop hook and
  `state.PruneEvidenceDirs`); source showed a third, `execReapRunDirectories`,
  which reaps `runs/` subdirectories older than the TTL. The same doc's Marker
  Fields table omitted the `done` marker while its prose referred to "five
  planIntegrity keys"; `requiredPlanMarkers` checks exactly four keys, and
  `done` gates whether the check runs.
- Document references to config section names must stay in sync with config
  schema changes. When a config section is renamed, moved, or restructured
  (e.g. `[planStyle]` splitting into `[style]` + `[planStyle]`), audit every
  prose reference to that section in reference docs (headlines, connectivity
  tables, examples). A reference must name the actual section the code reads
  at that point, not the section's pre-restructure name.

## What NOT to flag

- SKILL.md prose drift against source — that is `skill-prose-accuracy`'s job.
- Drift between `docs/skills/*.md` and SKILL.md — that is `skill-doc-drift`'s
  job.

## Cross-references

- `skill-prose-accuracy.md` — same check for SKILL.md files.
- `skill-doc-drift.md` — `docs/skills/*.md` vs SKILL.md.
