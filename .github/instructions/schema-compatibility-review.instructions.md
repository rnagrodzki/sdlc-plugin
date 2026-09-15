---
applyTo: "plugins/sdlc/schemas/**,**/*_test.go"
---
# schema-compatibility-review — Review Instructions

When enum values, schema definitions, or properties are added, removed, or modified, tests with pinned references must be updated, and the Go code must support all schema values.

Default severity: medium

## Checklist

- [ ] For each enum value in the schema, grep internal/config/config.go to verify the code explicitly recognizes it (const, switch case, iota enum, or validation switch on that field type).
- [ ] For each new schema property, verify internal/config/config.go has the corresponding struct field with matching type and json tag.
- [ ] When schema is narrowed (enum values removed), grep _test.go for hardcoded references to the removed value and update them.
- [ ] When a schema enum, property, minItems, or other constraint changes, open `plugins/sdlc/templates/*.toml` and verify every default value and every comment-listed option still validates against the schema.
- [ ] Look for stale examples in templates (e.g., `rebase = auto` when schema only accepts boolean or prompt).
- [ ] If a schema property is removed, confirm the corresponding template entry is also removed or updated.
