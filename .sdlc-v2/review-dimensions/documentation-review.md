---
name: documentation-review
description: Contributor-facing docs under docs/ and the root README stay accurate as the setup/ship/review pipeline evolves.
triggers:
  - "docs/**"
  - "README.md"
severity: low
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
- Per this session's own memory note, architecture/contributor docs belong
  under `docs/`, not under `skills/` (which is runtime-only) — flag any doc
  content added to `plugins/sdlc/skills/` that reads as contributor-facing
  background rather than skill instructions.
