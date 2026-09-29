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
severity: high
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
- When a file implements two or more near-identical polling or retry loops,
  verify they agree on probe-vs-timeout ordering, error classification, and
  final-probe-on-timeout behavior; if a fix is applied to one loop, mirror it
  to the other or consolidate the loops to prevent drift.
- When handler code calls `os.ReadFile`, `os.Stat`, `filepath.ReadDir`, or
  similar filesystem probes, errors must be classified: use
  `errors.Is(err, fs.ErrNotExist)` to distinguish "file does not exist" from
  "permission denied", "I/O error", or other conditions. Never report all
  filesystem read errors uniformly as "file not found". Additionally, do not
  silently fall back to a default value or built-in template when a
  configured file is unreadable — surface the error to the caller so they
  can choose recovery.

## File-system operation error discrimination

When reviewing code that uses `os.Stat`, `os.Open`, `filepath.ReadDir`, `filepath.Walk`, or similar file-system probes, verify that the code distinguishes "file or directory does not exist" from "other errors":

- Use `errors.Is(err, fs.ErrNotExist)` or `os.IsNotExist(err)` explicitly. Do not collapse this condition into a generic error handler that treats "file missing" the same as "permission denied" or "I/O error".
- Code that maps a file-system probe result to a benign zero-value (e.g., `if err != nil { return empty; }`  for a "not found" path) must first check for terminal errors like `execx.ErrOutputCap` via `errors.Is`, and propagate them rather than downgrading them to "not found."

## Config-read error discrimination

When handler code reads configuration via `config.Read`, `config.ReadSection`, or `configReadSection`, errors must be explicitly classified:

- Use `errors.Is(err, config.ErrNotFound)` to distinguish "section does not exist" (benign) from "actual read/parse/permission error" (requires propagation or explicit handling).
- A handler that maps a config-not-found result to a benign zero-value (e.g., "not configured") must check `errors.Is(err, config.ErrNotFound)` first and distinguish it from parse errors (`TOML syntax error`), permission errors (`access denied`), or other real failures. Never collapse these conditions — a malformed `.sdlc-v2/local.toml` or `.sdlc-v2/config.toml` must surface to the caller as an error, not silently treated as "not configured". Code that silently downgrades config parse/permission errors to "value not set" masks misconfiguration that the user should fix.

## Type assertion error handling in critical paths

When handler code performs a type assertion on a value that could be of the wrong type, use the two-result form: `value, ok := interfaceValue.(ConcreteType)`. Never drop the `ok` result in code paths where the type matters. If `ok` is false, the operation has failed and must be propagated as an error (DomainError, InfraError, or DataError with actionable Suggestion), not silently treated as a zero-value or absent result. Example: `validateCheckpointData` incorrectly dropped the `ok` result from a type assertion, causing incorrect behavior to pass undetected. Every type assertion in MCP handler logic must check and handle the false case explicitly.

## Error-swallowing pattern in MCP handler operations

MCP tool handlers must not silently treat operation errors as absent or default:
- When a code path can fail (config reads, type assertions, parsing, external calls), the error must be propagated as a structured response (DomainError, InfraError, DataError with Suggestion), not dropped or converted to "not configured" or empty.
- Pattern to catch: `loadPlanStyle` discarded config-read errors without distinguishing ErrNotFound from parse/permission errors, causing misconfiguration to be silently treated as "not configured". Always use `errors.Is(err, config.ErrNotFound)` to distinguish benign absence from actual read/permission/parse errors, then propagate non-benign errors.
- Every error-returning operation in a handler has a caller who needs to know if it failed. Swallowing the error removes the caller's ability to recover or inform the user.
- Three-outcome load contracts must stay three outcomes: when a callee returns found / not-found / error (e.g. `(nil, nil)` for not-found and `(nil, err)` for a read or decode failure), the caller must keep all three distinct. Never map the error outcome to the same "not found" response, because the caller then gets the wrong recovery suggestion. Example: `evidenceLoadRun` discarded the error from `state.LoadRun` and reported a corrupt or permission-denied run file as "plan run not found".
- Decode failures are not benign absence: never map a `json.Unmarshal`, `json.Marshal`, or TOML/YAML parse error to nil or a zero value as if the data were never written. Propagate it as a structured error with an actionable Suggestion. Example: `evidenceCheckpoint` returned nil on a decode failure, so a corrupt checkpoint looked the same as "never checkpointed".
