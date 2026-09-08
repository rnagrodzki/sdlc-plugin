---
name: error-handling-review
description: Sentinel-error and retry/backoff discipline established by internal/execx should extend consistently to other packages that shell out or call external services.
triggers:
  - "internal/execx/**"
  - "internal/tools/**"
  - "internal/jirakeys/**"
  - "internal/ghx/**"
  - "internal/gitx/**"
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
