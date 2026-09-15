---
name: schema-compatibility-review
description: When enum values, schema definitions, or config properties are added, removed, or modified, verify Go code supports the schema values and all tests are updated accordingly.
triggers:
  - "plugins/sdlc/schemas/**"
  - "**/*_test.go"
severity: medium
---

- Tests whose name contains "Enum", "Schema", or "Checksum" are casualties of any schema-narrowing task -- search for them by schema filename before the task is dispatched.
- When removing an enum value, grep _test.go files in the same directory for that literal value before deletion.
- When tightening a schema constraint, verify all test cases asserting the old constraint are updated.
- When adding or modifying enum values in the schema (e.g. version.method: "push-with-secret", version.pushAuth), verify the Go code in internal/config/ recognizes these values by grep for the field type and enum switch/const that validates it.
- For schema property additions (e.g. pushAuth: {type: object}), verify internal/config/config.go has the corresponding struct field on the config type.
