---
name: schema-config-change-impact-review
description: Config and schema structure changes must identify all secondary readers (migrators, codegen, test fixtures) alongside primary writers
severity: high
triggers:
  - 'internal/config*/**/*.go'
  - 'internal/**/config*.go'
  - 'plugins/sdlc/schemas/**'
  - 'plugins/sdlc/templates/**'
---

## Scope
- Identify all direct readers via grep and package imports
- Identify secondary consumers: migrators (internal/configmigrate), codegen, test fixtures, validators
- Verify each secondary consumer explicitly handles the schema version
- Run full module tests (go test ./...) to surface cross-package regressions
- Template files in `plugins/sdlc/templates/*.toml` are secondary readers
  of the config schema — when schema field names, enum values, or structure
  changes, validate that corresponding template comments, defaults, and
  field names stay synchronized.
- CI workflow files (`.github/workflows/*.yml`) and doc files (`docs/**`,
  `README.md`) that reference schema/config field names or paths are also
  secondary readers — check they stay in sync with a schema/config change.
- When any Go struct serialized to JSON (especially `*In`/`*Out` structs
  for MCP tools) gains new exported fields, the corresponding JSON schema
  under `plugins/sdlc/schemas/` MUST be updated in the same change to
  document the new fields. Schema-code drift — schemas lacking new fields
  that the Go code writes, or vice versa — breaks downstream consumers.
  Audit: for each new exported field added to a serialized struct, grep the
  schema file to confirm a matching property definition exists with
  `jsonschema_description`.
