---
name: security-review
description: Injection safety of the centralized subprocess-execution chokepoint and secret handling for Jira/GitHub/git credentials.
triggers:
  - "internal/execx/**"
  - "internal/jirakeys/**"
  - "internal/ghx/**"
  - "internal/gitx/**"
skip-when:
  - "**/*_test.go"
severity: high
---

This project shells out to `git`/`gh` and calls the Jira REST API through a
single centralized execution chokepoint (`internal/execx`) — every new
subprocess call site is a place injection or credential-leak bugs can enter.
Check:

- Every subprocess invocation goes through `internal/execx` rather than a
  new, ad-hoc `exec.Command` call site. A new raw `exec.Command` outside
  `execx` is a red flag — the whole point of the chokepoint is that there is
  exactly one place that builds and runs external commands.
- Arguments passed to git/gh commands are never built by string
  concatenation/interpolation of user- or repo-derived content (branch
  names, commit messages, PR titles, Jira issue text) — they must be passed
  as separate argv elements, never through a shell.
- Configured values and user-supplied strings (branch names, tag names,
  commit refs) must be validated before they reach a git/gh command, even
  as separate argv elements: a value starting with `-` is parsed as a flag.
  Route them through `gitx`'s `validateRef` (or an equivalent check) at the
  function that returns them, so every caller is covered — not only the
  callers that happen to validate first.
- `internal/jirakeys` never logs, echoes, or writes an API key/token to a
  file or MCP tool response body. Check any new code path that reads a key
  from this package for accidental inclusion in error messages or debug
  output.
- Retry/backoff logic added near `execx.Retry` does not swallow or
  downgrade `execx.ErrOutputCap` — the package doc is explicit that
  output-cap overflow must never be silently downgraded to a soft failure.
- Any new environment variable or config field that carries a credential is
  excluded from `.sdlc-v2/config.json` (which is typically committed) and
  lives only in `.sdlc-v2/local.json` or the OS environment.
