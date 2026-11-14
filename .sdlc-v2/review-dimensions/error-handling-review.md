---
name: error-handling-review
description: Sentinel-error and retry/backoff discipline established by internal/execx should extend consistently to other packages that shell out or call external services.
triggers:
  - "internal/execx/**"
  - "internal/tools/**"
  - "internal/jirakeys/**"
  - "internal/ghx/**"
  - "internal/gitx/**"
  - "internal/config/**"
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
- An error or warning that carries the output of an external program (`gh`, `git`, `openspec`) keeps the trimmed stderr, cut to a named constant, in the `Cause` or a detail field. The `Msg` names the operation and the failure class. A handler picks the failure class from the exit code and a typed sentinel (`execx.ErrOutputCap`, a missing binary) before it matches any stderr text. A stderr match is anchored to the documented phrase of the program, not to a short substring such as `auth` that a repository name can hold. A default branch that copies the whole stderr into `Msg` is a finding. A caller-visible class that rests on an unanchored substring is a finding. This extends the string-matched-error bullet and the sentinel bullets above, which do not bound the stderr text or say how a failure class is chosen. Example: a `gh issue view` failure classified by substring, with the default branch putting the whole stderr in `Msg`.
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
- A layer that turns a typed error into another form for the caller (an HTTP
  response body, a wrapped `error`, a rendered line) carries the `Msg`, the
  `Suggestion`, the class and the `Cause` across. A conversion that keeps the
  `Msg` and drops the `Suggestion`, or that maps every class to one status or
  one text, is a finding: the caller loses the recovery step that the handler
  wrote. For every route or entry point that serves an error, read the code
  between the `DomainError`, `InfraError` or `DataError` and the bytes that the
  caller receives, and check that a test reads the `Suggestion` from the real
  response. This extends the actionable-response bullet above: that bullet
  asks a handler to write a remediation hint, not that the hint reach the
  caller. Example: a detail route set a `Suggestion` on its error and the
  response held only the message.
- A step that sorts a failure into kinds (an enum such as rejected, no upstream, auth, other) gives each kind its own `Suggestion`, and the text of each names the step that changes the cause of that kind. One `Suggestion` that every kind shares, or a default text for kinds that need different recovery, is a finding: a caller who gets the wrong recovery step cannot fix the failure. Read the code between the classifier and the error it builds, and list each kind with its text. Each kind has one test that asserts the kind, the `Msg`, the `Suggestion`, and the cause with `errors.Is` or `errors.As`. This extends the bullet above that a conversion mapping every class to one text is a finding, which names a layer that converts a typed error, not a classifier of the failures of one program. Example: a push helper that returned one `Suggestion` for a rejected push, a missing upstream and a failed lease.
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
  to the other or consolidate the loops to prevent drift. The same holds for
  two or more loops or passes over one set of items (the files of one snapshot,
  the rows of one table): each pass treats a per-item failure alike. An item
  that is gone when a pass reads it (`errors.Is(err, fs.ErrNotExist)` after a
  listing) gives the same result and the same warning text in every pass. A
  pass that warns while its sibling skips the item with no signal is a finding.
  Prefer one loop that all passes share. Example: a file deleted during a
  snapshot gave a warning in one loop only.
- A reader or builder that turns a failed read into an empty list, a zero count
  or `(none)` shows the user a false answer: a page that lists no deferred items
  because `deferred.json` could not be read looks the same as a store with no
  items. Only `errors.Is(err, fs.ErrNotExist)` may give the empty result. Any
  other read error reaches the user as a structured error, or as a warning that
  names the source, the cause and the value used instead. When several builders
  read different sources for one output (for example the cache builders of one
  snapshot), each one treats a read error the same way. A fix at one source that
  leaves a sibling source dropping the error is a finding: check every source,
  not only the one that the change names. This extends the benign zero-value
  bullet and the per-item failure bullet above, which cover terminal sentinels
  and passes over one set of items. Example: a deferred-items read error shown
  as an empty backlog, while the other cached sources dropped their read errors
  too.
- A helper that answers a yes-or-no question about a file or folder from
  `os.Stat`, `os.ReadDir` or a similar probe (a folder check, an archive check,
  an artifact check) and returns `false` or an empty list for every error is a
  finding. Only `errors.Is(err, fs.ErrNotExist)` may give the negative answer.
  Any other error (permission denied, an I/O error, a path that runs through a
  file) reaches the caller as an `error`, or as a warning that names the path,
  the cause and the value used instead. A caller that then reports 'not a
  folder', 'no archive' or 'artifact missing' tells the user a false cause. The
  same holds for the `os.Stat` that reads the old mode before a rewrite: an
  ignored stat error resets the mode with no signal. This extends the
  reader-or-builder bullet above, which names readers and builders and not
  yes-or-no predicates. Example: a folder check that mapped every stat error to
  'not a folder', and an archive check that read every `ReadDir` error as 'no
  archive'.
- When handler code calls `os.ReadFile`, `os.Stat`, `filepath.ReadDir`, or
  similar filesystem probes, errors must be classified: use
  `errors.Is(err, fs.ErrNotExist)` to distinguish "file does not exist" from
  "permission denied", "I/O error", or other conditions. Never report all
  filesystem read errors uniformly as "file not found". Additionally, do not
  silently fall back to a default value or built-in template when a
  configured file is unreadable — surface the error to the caller so they
  can choose recovery.
- Pick the error class by cause, following the Error codes table in
  `docs/internal/mcp-output-contract.md`: `DomainError` for bad caller input,
  `InfraError` for filesystem, network, or process failures, `DataError` for
  parse or schema failures. A filesystem read failure returned as
  `DomainError` is a finding. Example: `openspec_save` mapped every plan read
  failure to a `DomainError`. The reverse is a finding too: a parse or rule
  failure of content the user wrote (a plan, a config file, a task body)
  returned as `InfraError`, because an `InfraError` reads as a plugin or
  environment fault and gives the user no text to fix. Return `DomainError` or
  `DataError` by cause, with a Suggestion that names the heading or line to
  fix. A runner of independent checks (for example a set of gates) records an
  error from one check as the result of that check and runs the others; one
  check's input error MUST NOT abort the batch. Example: plan-text errors
  returned as `InfraError` aborted all gate checks.
- Do not build an error message by string-replacing text in another error's
  message. Keep the failing values typed and format this call site's message
  from them. Example: `openspecSaveMaterializeError` edited its error text by
  string replacement.
- A handler or helper that tries more than one candidate target in turn (for
  example the main root, then the active worktree) reports every failed
  attempt when all of them fail: the returned error names each target and its
  cause. An error from an earlier attempt that a later attempt overwrites, that
  the code drops before the return, or that the code passes to a later function
  as an `error` parameter is a finding. Handle it where it arises, collect it
  in a list, or join it with `errors.Join`. This extends the chain-preservation
  bullet above, which keeps the chain of one error but not the errors of
  several attempts. Example: `openspec_save` lost the main-worktree error when
  the save failed.
- A typed error with no `Cause` needs a stated ground. When the branch holds a
  lower-level error (an `err` from a read, a stat, a parse or a call), the typed
  error that the branch returns sets `Cause` to it, also when a helper builds
  the error: the helper takes the error as a parameter. When no lower-level
  error exists (the code found an empty list, a missing record or a state that
  breaks a rule, and no operation failed), an `InfraError` is the wrong class,
  because it reads as a failed filesystem, network or process operation. Return
  a `DomainError` when the caller's input is wrong, or a `DataError` when stored
  data breaks a rule. An `InfraError` with no `Cause` is a finding on one of
  these two grounds. This extends the `Cause` bullet and the error-class bullet
  above: the first asks for `Cause` when an error is wrapped, and the second
  names two class mismatches but not an `InfraError` raised for a state.
  Example: a helper for a 'no topic file' state returned an `InfraError` with
  no `Cause`.
- A step that runs on a best-effort basis (its failure becomes a warning and the flow goes on, such as a push after a commit) keeps the error discipline of a required step. Walk each call of the step in order, including each lookup that runs before the main call (the current branch, a remote name, the default branch). (1) A lookup that fails is reported as a failure of that lookup, with its cause. The warning does not use the failure class of the main call. Example: a failed branch lookup reported as 'push failed (other)'. (2) A lookup of `git` or `gh` state that returns an empty string, `false` or a default value for every error is a finding, for the same reason as the file-probe bullet above. A documented not-found answer may give the empty result. Any other error becomes a warning that names the lookup, the cause and the value used instead. (3) Program output (stderr) in a warning is cut to a named constant and passes through the shared masking helper that the `security-review` dimension names. (4) A warning that says what a later step does (for example that the pr step pushes again) holds on every route that reaches the warning, or it states its condition. Read the later step to check. (5) Each failure arm of the step has a test that asserts the warning text and the cause. This extends the file-probe bullet above, which names `os.Stat` and `os.ReadDir` probes and not a `git` lookup, and the error-message accuracy section, which does not name a warning that promises later work. Example: a best-effort push that reported a failed branch lookup as 'push failed (other)', ignored a failed default-branch lookup, and promised a second push that one route skipped.

## File-system operation error discrimination

When reviewing code that uses `os.Stat`, `os.Open`, `filepath.ReadDir`, `filepath.Walk`, or similar file-system probes, verify that the code distinguishes "file or directory does not exist" from "other errors":

- Use `errors.Is(err, fs.ErrNotExist)` or `os.IsNotExist(err)` explicitly. Do not collapse this condition into a generic error handler that treats "file missing" the same as "permission denied" or "I/O error".
- Code that maps a file-system probe result to a benign zero-value (e.g., `if err != nil { return empty; }`  for a "not found" path) must first check for terminal errors like `execx.ErrOutputCap` via `errors.Is`, and propagate them rather than downgrading them to "not found."

## Config-read error discrimination

When handler code reads configuration via `config.Read`, `config.ReadSection`, or `configReadSection`, errors must be explicitly classified:

- Use `errors.Is(err, config.ErrNotFound)` to distinguish "section does not exist" (benign) from "actual read/parse/permission error" (requires propagation or explicit handling).
- A handler that maps a config-not-found result to a benign zero-value (e.g., "not configured") must check `errors.Is(err, config.ErrNotFound)` first and distinguish it from parse errors (`TOML syntax error`), permission errors (`access denied`), or other real failures. Never collapse these conditions — a malformed `.sdlc-v2/local.toml` or `.sdlc-v2/config.toml` must surface to the caller as an error, not silently treated as "not configured". Code that silently downgrades config parse/permission errors to "value not set" masks misconfiguration that the user should fix.
- When the calling tool's output struct has no field to carry a non-fatal warning (e.g. a `ValidateOut` with no `warnings` array), a discarded config-read error must still be routed somewhere the user can see it — either surfaced as a finding on that same call, or documented inline naming the sibling call/action that already surfaces the identical warning. A silent discard with no routing note is a finding even when a sibling action happens to cover it.

## Type assertion error handling in critical paths

When handler code performs a type assertion on a value that could be of the wrong type, use the two-result form: `value, ok := interfaceValue.(ConcreteType)`. Never drop the `ok` result in code paths where the type matters. If `ok` is false, the operation has failed and must be propagated as an error (DomainError, InfraError, or DataError with actionable Suggestion), not silently treated as a zero-value or absent result. Example: `validateCheckpointData` incorrectly dropped the `ok` result from a type assertion, causing incorrect behavior to pass undetected. Every type assertion in MCP handler logic must check and handle the false case explicitly.

- A small accessor that reads a key from a decoded map or an `any` value
  (for example a string or bool getter over a `detail` map) and returns the zero
  value both for a missing key and for a value of the wrong type is the same
  defect as a type assertion that drops `ok`. The caller cannot tell 'absent'
  from 'wrong type'. For a key that decides an action (a sync flag, a change
  name, a branch), the code uses a form that reports the wrong type: the call is
  refused with a `DomainError` that names the key, the type found and the type
  expected, or the output carries a warning with the same three facts and the
  value used instead. A test passes each wrong type (a string for a bool, a
  number for a string) and asserts the refusal or the warning. This extends the
  type-assertion section above, which names the two-result assertion and not the
  helper that wraps it. Example: a sync flag read with a bool accessor that
  ignored a string value, and a change name read with a string accessor that
  turned a number into an empty name.

## Error-swallowing pattern in MCP handler operations

MCP tool handlers must not silently treat operation errors as absent or default:
- When a code path can fail (config reads, type assertions, parsing, external calls), the error must be propagated as a structured response (DomainError, InfraError, DataError with Suggestion), not dropped or converted to "not configured" or empty.
- Pattern to catch: `loadPlanStyle` discarded config-read errors without distinguishing ErrNotFound from parse/permission errors, causing misconfiguration to be silently treated as "not configured". Always use `errors.Is(err, config.ErrNotFound)` to distinguish benign absence from actual read/permission/parse errors, then propagate non-benign errors.
- Every error-returning operation in a handler has a caller who needs to know if it failed. Swallowing the error removes the caller's ability to recover or inform the user.
- Three-outcome load contracts must stay three outcomes: when a callee returns found / not-found / error (e.g. `(nil, nil)` for not-found and `(nil, err)` for a read or decode failure), the caller must keep all three distinct. Never map the error outcome to the same "not found" response, because the caller then gets the wrong recovery suggestion. Example: `evidenceLoadRun` discarded the error from `state.LoadRun` and reported a corrupt or permission-denied run file as "plan run not found".
- Decode failures are not benign absence: never map a `json.Unmarshal`, `json.Marshal`, or TOML/YAML parse error to nil or a zero value as if the data were never written. Propagate it as a structured error with an actionable Suggestion. Example: `evidenceCheckpoint` returned nil on a decode failure, so a corrupt checkpoint looked the same as "never checkpointed".
- A parser or scanner of stored or user-written text (a table of rows, a list of lines, a block of items) that drops a row or line which does not match its pattern, with no signal, is a finding. A damaged row looks the same as a row that was never written, so the caller reads a partial result as the whole result. The function counts the skipped rows and returns them as a typed value (a count, the line numbers, a warning field). The output then warns with the count, a quote of the first skipped row and the effect on the result. An input with no matching row at all reports 'nothing parsed', never a clean empty result. A test puts one damaged row among valid rows and asserts the warning and that the valid rows are still read. This extends the decode-failure bullet above, which names a failed decode and not a row that fails a pattern inside text that decodes. Example: Decisions table rows and Smallest cut lines that were dropped with no warning.

## Error-message accuracy in InfraError Msg

When wrapping a shell command failure into an `InfraError`, the `Msg` field must name only the subcommand and flags that were actually invoked on that code path. Do not hardcode a message listing all possible flags (e.g., `'gh pr edit --add-label/--remove-label'`) when the conditional branch only appended one of them. Derive the message from the args slice actually sent: if `--remove-label` was not appended on this branch, omit it from Msg. This ensures the error message accurately describes the command that was attempted.

## Uniform error enrichment across gh call sites

When a tool's description promises uniform enrichment for a class of `gh` failures (e.g., "account-switch guidance for any gh permission error"), verify that every `gh` invocation site in the handler that can produce that failure calls the same enrichment helper. Grep the handler for each `gh` call and trace its error path — if one site calls `prEnrichPermissionError` and another does not, a code branch is not delivering the promised behavior. When adding a new `gh` call site, trace the failure to the enrichment helper used elsewhere and use the same one consistently.

## Error-message accuracy across failure paths

When a callee can fail for multiple distinct causes (e.g., splice failures distinguish layout-mismatch from parse error from read error), the caller must either: (1) receive the cause via sentinel/typed error and select a cause-specific recovery message, or (2) emit a cause-neutral message that applies across all failure paths. A cause-specific error message on a code branch that does not check the actual cause is a finding — the message may not reflect what actually failed. Example: reporting "could not rewrite table keys" when the actual failure was a file-read permission error violates this contract. Error message text must be derived from the actual condition detected on that code path, never hardcoded to assume one failure type when multiple are possible.
