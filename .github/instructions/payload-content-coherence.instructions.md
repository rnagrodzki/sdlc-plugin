---
applyTo: "internal/tools/payloads/**,.github/scripts/**"
---
# payload-content-coherence — Review Instructions

Error hints and log messages in scaffolded CI payloads (internal/tools/payloads/*.cjs, .github/scripts/*.cjs) must match the values the scaffolder rewrites and must make sense inside a consumer repo.

Default severity: high

## Checklist

- Hints that name config-controlled values (secret name, method, prefix) read that value at runtime or take it as a parameter. They never hardcode a value that `scaffold_ci` rewrites.
- Doc links in hints use absolute `https://` URLs, not relative paths that are dead in consumer repos.
- Recovery hints cover every reachable state. Do not assert one cause unconditionally when a configured App or PAT is also possible.
- Each option a hint offers helps for the rejected ref. Do not offer `version.method = "pr"` for a tag-push rejection.
- Cleanup steps that fail after a failure log a WARNING, not a swallowed error.
- Cross-cutting consistency: when a classifier, hint, error branch or shared helper is added to one payload, check that every sibling payload sharing the same push helper or token chain gets it too. Payloads are copy-pasted, not imported.
