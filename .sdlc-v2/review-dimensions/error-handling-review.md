---
name: error-handling-review
description: Sentinel-error and retry/backoff discipline established by internal/execx should extend consistently to other packages that shell out or call external services.
triggers:
  - "internal/execx/**"
  - "internal/tools/**"
  - "internal/jirakeys/**"
  - "internal/ghx/**"
  - "internal/gitx/**"
  - "plugins/sdlc/skills/**"
severity: medium
---

`internal/execx` sets the pattern for this codebase: a distinct sentinel
error (`ErrOutputCap`) that is never silently downgraded to a soft failure,
plus a documented `Retry` helper with 3 retries and 1s/2s/4s backoff.
Several `internal/tools/*.go` files already follow this with their own
`errors.Is`/`errors.As`/`var Err...` sentinel patterns. Check:

- New error conditions that callers need to branch on are exposed as
  sentinel errors (`var ErrX = errors.New(...)`) or typed errors checkable
  with `errors.As`, not as bare string-matched error text.
- Wrapping preserves the chain (`%w`, not `%v`) so `errors.Is`/`errors.As`
  keeps working through intermediate layers (MCP tool handler wrapping a
  lower-level package error).
- Retry logic added elsewhere doesn't silently swallow a terminal error
  (like `execx.ErrOutputCap`) inside a retry loop — a caller needs to be
  able to tell "retried and still failed" apart from "succeeded on a later
  attempt."
- MCP tool handlers in `internal/tools/**` translate lower-level errors
  into actionable tool-error responses rather than leaking a raw Go error
  string with no remediation hint (see also `mcp-tool-review`).
- External-service call sites (`internal/jirakeys`, `internal/ghx`,
  `internal/gitx`) distinguish transient failures (network, rate limit —
  worth retrying) from permanent ones (bad credentials, 404 — not worth
  retrying) rather than treating every error identically.
- Call sites that map an error to a benign zero-value result (probes returning
  false, lookups returning empty) must first check terminal sentinels via
  `errors.Is` (e.g., `execx.ErrOutputCap`) and propagate them — never
  downgrade a terminal error into "not found / no access."
- Package documentation must accurately reflect the error-handling semantics
  that callers actually implement, not speculative retry behavior.
- Skills that invoke external tools (`gh`, `docker`, `jira`, `git`) must
  document what each non-zero exit code means and distinguish
  transient/retryable errors (network timeouts, rate limits, checks
  pending) from permanent failures (bad credentials, 404, not found).
  Example: `gh pr checks` returns exit code 8 when checks are still
  running — this must be handled as "transient, poll later", not
  "permanent failure." Skills must warn users about machine-global or
  out-of-scope side effects of external tool commands (e.g. `gh auth
  switch` is global to the machine and persists after the script ends, not
  scoped to the repo or session).
- Do not discard errors from file-system operations (`os.WriteFile`,
  `os.Remove`, `os.MkdirTemp`) or state-persistence operations
  (`state.Write`, state reads) when the next line reports success to the
  caller — never discard a file-system or state-persistence error when the
  next line reports success to the caller.
- When wrapping an error into a structured error type (`DataError`,
  `DomainError`, `InfraError`), the `Cause` field MUST be populated with
  the original error — never omit it. This preserves the error chain for
  `errors.Unwrap()` and debugging.
- Functions that return `(T, error)` and use nil-error-with-zero-T to
  signal not-found (as opposed to an actual error) MUST document this
  three-outcome contract in package godoc: found (value, nil), not-found
  (zero, nil), error (zero, error). Callers must handle all three cases
  explicitly and never collapse not-found into the same path as error.
