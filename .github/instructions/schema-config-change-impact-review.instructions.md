---
applyTo: "internal/config*/**/*.go,internal/**/config*.go,plugins/sdlc/schemas/**,plugins/sdlc/templates/**"
---
# schema-config-change-impact-review — Review Instructions

Config and schema structure changes must identify all secondary readers (migrators, codegen, test fixtures) alongside primary writers

Default severity: high

- Verify that config template files in `plugins/sdlc/templates/`
  (config.toml, local.toml) have field names and enum examples that match
  the updated schema — renamed schema fields must be reflected in template
  comments and defaults.
