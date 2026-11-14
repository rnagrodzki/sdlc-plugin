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
- Every value that a function passes to a subprocess as an argument is validated in the function that accepts it, not only refs. This covers a remote name, the expected value of a lease option, a path and a URL. List each parameter that reaches the argument list of `git` or `gh`. For each one, the function refuses an empty value, a value that starts with `-`, a value with whitespace or a control character, and a character that the program reads in a special way in that position (for example `:` or `=` in a lease argument). The error names the value and the rule. A parameter that is safe only because today's callers pass a clean value is a finding, because the next caller can pass anything. Each refusal has one test that uses a fake runner and asserts that the program was not run. This extends the previous bullet, which names branch names, tag names and commit refs only. Example: a push function that took a remote name and a lease value and passed both to `git` unchecked.
- `internal/jirakeys` never logs, echoes, or writes an API key/token to a
  file or MCP tool response body. Check any new code path that reads a key
  from this package for accidental inclusion in error messages or debug
  output.
- Retry/backoff logic added near `execx.Retry` does not swallow or
  downgrade `execx.ErrOutputCap` — the package doc is explicit that
  output-cap overflow must never be silently downgraded to a soft failure.
- Any new environment variable or config field that carries a credential is
  excluded from `.sdlc-v2/config.toml` (which is typically committed) and
  lives only in the local, uncommitted config file or the OS environment.
- Code that masks a secret before text leaves the process (the userinfo of a URL, a token or a password in the stderr of `git` or `gh`, in an error `Msg`, a `Cause`, a warning or a log line) uses a pattern that covers the whole secret. A password or token can hold `@`, `:`, `/` or a percent escape, so a pattern that stops at the first `@` or `/` leaves part of the secret in the text. Parse the URL, or match to the last `@` before the host. Every site that prints or stores the output of an external program calls one masking helper. A test gives the helper a secret with each of these characters, two secrets in one line, a secret at the start and at the end of the line, and a text with no secret, and asserts the full output. A masking helper that is tested with one plain secret is a finding. This extends the credential bullet above, which names `internal/jirakeys` and not text copied from another program. Example: a redaction regexp that stopped at the first `@` of the userinfo.
