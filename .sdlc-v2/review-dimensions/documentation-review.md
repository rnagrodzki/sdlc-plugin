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
  `maxParallelDimensions`), the corresponding docs section must state the
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
- Every limit, threshold, or cap that a doc states, including a hardcoded one
  with no config key, names the set it counts (which items, in which folder,
  per call or in total), the unit, and what happens at the limit (rejected,
  truncated, or ignored). A bare number such as 'at most 100 files' with no
  counted set is a finding. Check the counted set against the code that
  enforces the limit, and check that every other page that states the same
  limit gives the same scope. This extends the config-controlled limit bullet
  above.
- A name or label that a doc gives to a value the code derives (a duration, a
  count, an age, a total) states what the code computes. A span names its start
  point, its end point and its unit, and says what it leaves out. A count names
  what it counts. Read the code that computes the value and compare it with the
  label. A label that names a nearby quantity is a finding. Example: a
  'Duration' column that holds the span from a start stamp to the last file
  change shows elapsed time between two stamps, not time spent.
- A page under `docs/` that states what a tool or skill does, never does, keeps, removes or exposes (for example 'the tool never removes an entry', 'no tool returns the stored table') is checked against the Go handler, not only against SKILL.md. Walk each mode and input form that the handler accepts, and each tool that is registered. A case that breaks the claim is a finding. This applies the negative-claim check of `skill-prose-accuracy` to docs pages. The `skill-doc-drift` check compares docs with SKILL.md only, so a claim that both texts repeat and the code breaks passes it.
