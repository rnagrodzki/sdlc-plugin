---
name: schema-compatibility-review
description: When enum values or schema definitions are removed or narrowed, tests with pinned references to those values must be updated or removed.
triggers:
  - "plugins/sdlc/schemas/**"
  - "**/*_test.go"
severity: medium
---

- Tests whose name contains "Enum", "Schema", or "Checksum" are casualties of any schema-narrowing task -- search for them by schema filename before the task is dispatched.
- When removing an enum value, grep _test.go files in the same directory for that literal value before deletion.
- When tightening a schema constraint, verify all test cases asserting the old constraint are updated.
