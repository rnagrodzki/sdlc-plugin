---
name: documentation-review
description: Contributor-facing docs under docs/ and the root README stay accurate as the setup/ship/review pipeline evolves.
triggers:
  - "docs/**"
  - "README.md"
  - "plugins/sdlc/skills/**"
  - "plugins/sdlc/schemas/**"
severity: medium
---

This project keeps contributor documentation in `docs/`
(`getting-started.md`, `local-install.md`, `plan-architecture.md`,
`smoke-test.md`) separate from the runtime skill content under
`plugins/sdlc/skills/`. Check:

- Behavioral changes to a skill (`plugins/sdlc/skills/**`) or an MCP tool's
  contract are reflected in `docs/` where relevant, rather than only in the
  skill's own inline prose.
- Code examples and CLI invocations in `docs/` still match current tool
  names and flags (e.g. `setup_prepare`, `--dimensions`) — a renamed tool or
  flag that isn't updated here will mislead the next contributor.
- New root-level scripts, directories, or config files (`.sdlc-v2/`,
  `lefthook.yml`) that a new contributor would need to know about are
  mentioned in `README.md` or `docs/getting-started.md`.
- When a task adds a config-controlled limit, threshold, or cap (e.g.
  `maxDimensions`), the corresponding docs section must state the
  configuration key, the default value, the valid range or constraint, and
  the procedure to change it. A schema description alone does not satisfy
  this — users need user-facing guidance, not only structured metadata.
- Per this session's own memory note, architecture/contributor docs belong
  under `docs/`, not under `skills/` (which is runtime-only) — flag any doc
  content added to `plugins/sdlc/skills/` that reads as contributor-facing
  background rather than skill instructions.
- On any configuration path or directory rename (e.g. `.sdlc/` ->
  `.sdlc-v2/`), perform a full-repository grep sweep across `docs/**`,
  `README.md`, `plugins/sdlc/skills/**`, and `plugins/sdlc/schemas/**` to
  catch all references and update them in the same task. Incomplete sweeps
  leave contributors following stale instructions.
- On any SKILL.md step reorder or phase relocation, enumerate every table,
  walkthrough, and lifecycle section in `docs/*-architecture.md` that lists
  that skill's steps by name or order; grep the skill name and check each
  table for the moved phase name and its tool calls at the relocated
  position. Incomplete updates leave contributors following stale order.
