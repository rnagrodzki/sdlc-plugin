# Spec Delta

## MODIFIED Requirements

### Requirement: Expected account check
The tool SHALL compare the active gh account with `[github] expectedAccount` from the merged personal settings (`.sdlc-v2/local.toml` over `~/.sdlc/local.toml`), ignoring case, and stop on a mismatch.

| Condition | Result |
|---|---|
| `expectedAccount` set, active account differs | `accountMismatch: true`; error `Expected gh account: <expected>` / `Active gh account:   <active>` / `Run: gh auth switch --user <expected>` |
| Mismatch diagnostics | `diagnostics.switchHint` `gh auth switch --user <expected>`; `diagnostics.matchedAccount` names a logged-in account that matches; `diagnostics.loginHint` only when none matches |
| `[github]` section missing | treated as not configured; no unreadable warning |
| `[github]` section unreadable | warning `.sdlc-v2/local.toml (or ~/.sdlc/local.toml) [github] section unreadable: <error>; skipping expected-account check` |

- `expectedAccount` in the output echoes the trimmed configured value.

#### Scenario: Account mismatch
- **WHEN** the active account is `wronguser` and `expectedAccount` is `correctuser`
- **AND** `correctuser` is logged in locally
- **THEN** `accountMismatch` is `true` and `diagnostics.matchedAccount` is `correctuser`
- **AND** `next` is `Switch GitHub account, then call pr_prepare again.`

#### Scenario: Unreadable local.toml
- **WHEN** reading `[github]` fails with a TOML parse error
- **THEN** `warnings` contains `.sdlc-v2/local.toml (or ~/.sdlc/local.toml) [github] section unreadable`


### Requirement: Repository access probe
The tool SHALL probe repository access only when no `expectedAccount` is configured and the `origin` remote URL parses to an owner and repo.

| Condition | Result |
|---|---|
| Probe runs | `gh api repos/<owner>/<repo> --hostname github.com -i --silent`; `repoAccessProbed: true`; `repoAccessible` set when known; `repoAccessStatus` set whenever an HTTP status was received |
| HTTP 200 | `repoAccessible: true`; the call continues |
| Access denied (HTTP 403 or 404, read from the stdout status line or from gh's `(HTTP <code>)` stderr error) | stop; `repoAccessible: false`; error lists `Active gh account: <a>`, `Cannot access: <owner>/<repo>`, then `Try: gh auth switch --user <login>` per account or `Run: gh auth login --hostname github.com`; `diagnostics.owner` set |
| Result unknown (no HTTP response, e.g. a network failure, or any other HTTP status) | `repoAccessible` unset; warning `Repo access probe failed (<reason>) — proceeding without access verification.`; `<reason>` is gh's first stderr line or `unexpected HTTP <code> from gh api`, and defaults to `network error` |
| No `expectedAccount` and no parsable `origin` | warning `Could not resolve expected gh account (no [github] expectedAccount in .sdlc-v2/local.toml (or ~/.sdlc/local.toml), no origin remote). Skipping active-account check.`; no probe |

#### Scenario: Remote present
- **WHEN** `expectedAccount` is unset and `origin` is `git@github.com:acme/widgets.git`
- **THEN** `repoAccessProbed` is `true`

#### Scenario: Repository not visible to the active account
- **WHEN** `gh api repos/acme/widgets` answers HTTP 404 and exits 1
- **THEN** `ok` is `false`, `repoAccessible` is `false`, and `repoAccessStatus` is `404`
- **AND** `errors` contains `Cannot access: acme/widgets`

#### Scenario: Network failure during the probe
- **WHEN** `gh api repos/acme/widgets` fails with `error connecting to api.github.com` and no HTTP response
- **THEN** `repoAccessible` is unset and the remaining checks run
- **AND** `warnings` contains `Repo access probe failed (error connecting to api.github.com)`

#### Scenario: No remote
- **WHEN** `expectedAccount` is unset and there is no `origin` remote
- **THEN** `repoAccessProbed` is `false`
- **AND** `warnings` contains the `Could not resolve expected gh account` line
