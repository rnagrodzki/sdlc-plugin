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
- When a reference document describes struct return shapes (e.g., the return
  value table for a tool action or the state structure of a component), verify
  that every exported field of the Go *Out struct is mentioned in the prose,
  either explicitly by name in tables/examples or in a documented catch-all
  note. A documented return shape that omits fields is a finding; use grep or
  struct inspection to enumerate the actual Go struct and confirm field-by-field
  parity with the prose. Example: if plan/state-format.md documents
  cleanup-pipeline's return shape, confirm it names every field in the Go
  cleanup pipeline output struct.
- When a reference document describes a persisted row or record (e.g., a
  history row written by plan_mark, a state snapshot row, a ledger entry
  written by recovery paths), verify that every field each writer produces is
  documented. Writers that produce a reduced set of fields (omitting fields
  present in other writers' rows) must be explicitly noted with the fields that
  writer emits and the reason for the reduction. Use grep to find all call
  sites that write the row/record and cross-check the prose against each
  writer's field set. A documented row with fields omitted by some writers but
  not others, without explicit per-writer field enumeration, is a finding.
- When a reference document has a lifecycle rule that says when a state file
  is created, updated, or deleted, the rule must name every action and every
  marker that writes the file. Grep the callers of the write function and
  list each variant the source accepts. Example: the plan `state-format.md`
  Lifecycle Rules for `plan_mark` must name the `planIntegrity` markers, the
  `checkpoint` marker, and the `review-round` marker, because each writes a
  different part of the state file. A lifecycle rule that names fewer writes
  than the source has is a finding.
- When a reference document states a name or id rule, it must restate the
  whole pattern from the source regex: the anchored first-character class,
  the allowed set for the other characters, and the length bound. Compare the
  prose to the Go regex part by part. A rule that lists the allowed
  characters but omits that the first character is limited to a subset of
  them (for example `writerIDRe`: the first character is a letter or a
  digit) is a finding.
- Precedence or conflict claims between two keys or flags (for example
  `steps` wins over `quick`) must match the validator branch that handles
  the pair (for example the reject path in `ship_prepare`). A pair the code
  rejects together must be documented as rejected, not as a winner.
- Every example block (config snippet, step list, flag line) must follow
  the rules the same doc states and list exactly the canonical set in the
  Go source. An example that contradicts its own prose, or omits or invents
  a canonical item, is a finding.
- Named git refs in prose (default branch, base branch, target branch) must
  match the ref the code reads for that step. A mismatch is a finding.

## What NOT to flag

- SKILL.md prose drift against source — that is `skill-prose-accuracy`'s job.
- Drift between `docs/skills/*.md` and SKILL.md — that is `skill-doc-drift`'s
  job.

## Cross-references

- `skill-prose-accuracy.md` — same check for SKILL.md files.
- `skill-doc-drift.md` — `docs/skills/*.md` vs SKILL.md.
