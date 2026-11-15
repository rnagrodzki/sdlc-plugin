---
name: schema-compatibility-review
description: When enum values, schema definitions, or config properties are added, removed, or modified, verify Go code supports the schema values and all tests are updated accordingly.
triggers:
  - "plugins/sdlc/schemas/**"
  - "**/*_test.go"
  - "internal/shipmeta/**/*.go"
  - "internal/setupmeta/**/*.go"
  - "internal/tools/ship.go"
severity: medium
---

- Tests whose name contains "Enum", "Schema", or "Checksum" are casualties of any schema-narrowing task -- search for them by schema filename before the task is dispatched.
- When removing an enum value, grep _test.go files in the same directory for that literal value before deletion.
- When tightening a schema constraint, verify all test cases asserting the old constraint are updated.
- When adding or modifying enum values in the schema (e.g. version.method: "push-with-secret", version.pushAuth), verify the Go code in internal/config/ recognizes these values by grep for the field type and enum switch/const that validates it.
- For schema property additions (e.g. pushAuth: {type: object}), verify internal/config/config.go has the corresponding struct field on the config type.
- When Go code holds its own list of config keys with a type, an enum or a numeric range (a form field list, a setup field table, a validator), compare each key's type, enum values, minimum and maximum with its schema entry. A range or a type that differs from the schema is a finding, unless a comment names the reason. A test that pins the Go type of such a value asserts the type that the schema states. Example: a Go minimum for a numeric key that differs from the schema minimum.
