---
name: schema-description-accuracy
description: Prose in JSON schema property descriptions, template comments and Go doc comments must match what the code actually needs, and every prose default must also be a structured schema "default" keyword.
triggers:
  - "plugins/sdlc/schemas/**"
  - "plugins/sdlc/templates/**"
  - "internal/config/**"
severity: high
---

Schema descriptions are part of the config contract. Users and LLMs provision tokens, pick values and trust defaults from this prose. A description that claims a narrower permission scope than the code needs, or a default that exists only in prose, leads to misconfiguration that fails later with no link back to the cause.

When a diff adds or changes a property description in `plugins/sdlc/schemas/sdlc-config.schema.json`, a comment in `plugins/sdlc/templates/*.toml`, or a doc comment on a struct in `internal/config/`, check:

- Permission-scope claims: grep the code that uses the value (for example the release payloads in `internal/tools/payloads/*.cjs` that call `gh pr create`, `gh pr merge` or `gh workflow run`) and confirm the stated scope covers every call. The schema description, the template comment and the Go doc comment must all state the same scope.
- Default claims: when prose says "default X", the property must also carry `"default": X`. Tooling that reads schema defaults never sees a prose-only default.
- Examples: an example value in prose must pass the property's own constraints (enum, pattern, type).

Do not wave off a stale description as "documentation only". A wrong description is worse than none.