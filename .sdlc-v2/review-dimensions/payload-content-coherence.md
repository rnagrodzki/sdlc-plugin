---
name: payload-content-coherence
description: Error hints and log messages in scaffolded CI payloads (internal/tools/payloads/*.cjs, .github/scripts/*.cjs) must match the values the scaffolder rewrites and must make sense inside a consumer repo.
triggers:
  - "internal/tools/payloads/**"
  - ".github/scripts/**"
severity: high
---

Scaffolded payloads are copied verbatim into consumer repos and run there as CI code. Hint text, log messages and comments inside them are read by consumer-repo maintainers, not by sdlc-plugin contributors. Check:

- A hint that names a config-controlled value (secret name, method, prefix) must read that value at runtime or take it as a parameter. Never hardcode a value that `scaffold_ci` rewrites elsewhere (for example, `RELEASE_TOKEN` when `version.pushAuth.secretName` is set).
- Doc links in hints must be absolute `https://` URLs. A relative path such as `docs/versioning.md` is a dead link in the consumer repo.
- A recovery hint must cover every reachable state. Do not assert one cause unconditionally (for example, "GITHUB_TOKEN is in use") when a configured App or PAT is also possible; add the "already configured: check bypass list and token expiry" branch.
- Every option a hint offers must help for the rejected ref. Do not offer `version.method = "pr"` for a tag-push rejection: that setting changes commit delivery, not tag pushes.
- Cleanup steps that run after a failure (rollback, tag delete) must log a WARNING when they fail. A swallowed cleanup error leaves state the next run trips over.
- Helpers are copy-pasted across payloads (release-on-main.cjs, promote-release.cjs, retag-release.cjs), not imported. A change to one copy must land in every copy, and the `.github/scripts/` mirror must stay byte-identical to `internal/tools/payloads/`. When a diff adds a cross-cutting feature (a classifier, hint, log message or error branch) to one payload, check every sibling that shares the same push helper or token chain. A partial application makes delivery paths behave differently.
- Error-swallowing helpers (`exec()` returning `null` on failure) must not feed a condition where `null` reads as a real negative answer (e.g. "not a release-bump commit"). Use the throwing helper (`execOrThrow`) when the command is expected to succeed.