---
name: schema-config-change-impact-review
description: Config and schema structure changes must identify all secondary readers (migrators, codegen, test fixtures) alongside primary writers
severity: high
triggers:
  - 'internal/config*/**/*.go'
  - 'internal/**/config*.go'
---

## Scope
- Identify all direct readers via grep and package imports
- Identify secondary consumers: migrators (internal/configmigrate), codegen, test fixtures, validators
- Verify each secondary consumer explicitly handles the schema version
- Run full module tests (go test ./...) to surface cross-package regressions
