---
applyTo: "plugins/sdlc/templates/**,plugins/sdlc/schemas/**"
---
# template-schema-sync — Review Instructions

Template files (plugins/sdlc/templates/*.toml) must not diverge from the JSON schema (plugins/sdlc/schemas/*.json) — defaults and comments must accurately reflect all enum constraints, required properties, minItems, and other schema validation rules.

Default severity: high
