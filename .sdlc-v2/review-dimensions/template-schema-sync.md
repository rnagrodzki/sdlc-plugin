---
name: template-schema-sync
description: Template files (plugins/sdlc/templates/*.toml) must not diverge from the JSON schema (plugins/sdlc/schemas/*.json) — defaults and comments must accurately reflect all enum constraints, required properties, minItems, and other schema validation rules.
triggers:
  - "plugins/sdlc/templates/**"
  - "plugins/sdlc/schemas/**"
severity: high
---

When a schema enum, property set, minItems, or other constraint changes, audit all template files:

- Every default value in templates must be a valid enum member (or valid type) per the schema.
- Every comment that lists valid options must enumerate exactly what the schema permits, not a stale subset.
- Examples in comments (e.g. `rebase = auto`) must validate against the schema.
- If a schema property is renamed or removed, the corresponding template entry must be updated or removed.

Mismatches here create user confusion: a user follows the template's guidance only to have their config rejected at runtime.
