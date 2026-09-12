---
name: plugin-architecture-review
description: This repo IS a Claude Code plugin — plugin.json, hooks.json, .mcp.json, and schemas must stay internally consistent.
triggers:
  - "plugins/sdlc/**"
severity: medium
---

`plugins/sdlc/` is a self-referential Claude Code plugin: this repository
both builds the `sdlc` MCP server and ships it as
`plugins/sdlc/.claude-plugin/plugin.json` with `plugins/sdlc/hooks/hooks.json`,
`plugins/sdlc/.mcp.json`, and `plugins/sdlc/schemas/*.schema.json`. Check:

- A new or renamed skill under `plugins/sdlc/skills/**` is registered
  wherever the plugin manifest or command routing expects it (skill name
  matches the directory, no orphaned `SKILL.md`).
- `plugin.json` version, MCP server entry, and any listed hooks stay
  consistent with what `hooks/hooks.json` and `.mcp.json` actually declare
  — a mismatch here breaks plugin install, not just this repo's own build.
- New JSON emitted by an MCP tool that is meant to be consumed elsewhere in
  the plugin (config sections, dimension metadata, guardrails) validates
  against its corresponding file in `plugins/sdlc/schemas/*.schema.json`;
  a schema that isn't updated alongside a field rename/addition is a
  silent contract break for anything validating against it.
- Hooks declared in `hooks/hooks.json` reference scripts/binaries that
  actually exist at the declared path and are executable.
- Changes to the plugin's own skill flow (e.g. `setup/SKILL.md`,
  `setup/setup-dimensions.md`) keep their documented flag-routing tables
  and "DO NOT" rules in sync with what the corresponding Go MCP tools
  (`internal/tools/setup.go`, `internal/setupmeta`) actually implement —
  this project has already shown drift here (see the `.sdlc` vs
  `.sdlc-v2` / `.yaml` vs `.md` descriptor mismatch flagged in
  `internal/setupmeta/sections.go`).
- Skill reference docs defining procedural keywords/markers (e.g.
  "COMPLETE:") must ensure a diff modifying some occurrences does not
  leave others orphaned; verify the entire file for remaining references
  when a diff targets specific line ranges rather than the whole file.
- Template files in `plugins/sdlc/templates/` must not diverge from
  canonical sources in `internal/tools/payloads/` or elsewhere; if a
  canonical payload is refactored, stale copies must be deleted rather
  than maintained in parallel. Duplicate templates in separate directories
  create silent drift risk.
