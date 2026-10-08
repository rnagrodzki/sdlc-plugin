---
applyTo: "plugins/sdlc/templates/**,plugins/sdlc/schemas/**"
---
# template-schema-sync — Review Instructions

Template files (plugins/sdlc/templates/*.toml) must not diverge from the JSON schema (plugins/sdlc/schemas/*.json) — defaults and comments must accurately reflect all enum constraints, required properties, minItems, and other schema validation rules.

Default severity: high

## Verification Checklist
- Each template key whose schema sets an enum, minItems, a bound, or a rejected empty value has a comment that states that constraint.
