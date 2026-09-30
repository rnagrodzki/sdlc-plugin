# tool-received-review-prepare Specification

## Purpose
MCP tool `received_review_prepare` gives the `received-review` skill a pull request overview: PR identity, the `gh pr view` text, and the `gh pr checks` text. It does not return review comments or classify threads. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Registration and annotations
The tool SHALL be registered as `received_review_prepare` with the title "Fetch PR review feedback", a description starting with `INTERNAL — called by sdlc skills only.`, and the annotations below.

| Annotation | Value |
|---|---|
| `ReadOnly` | `true` |
| `Idempotent` | `true` |
| `OpenWorld` | `true` |

#### Scenario: Client lists tools
- **WHEN** an MCP client lists the server's tools
- **THEN** `received_review_prepare` is present with `ReadOnly: true`, `Idempotent: true`, `OpenWorld: true`

### Requirement: Input field
The tool SHALL accept one input field, `pr`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `pr` | int | yes | JSON number, e.g. `42` | Pull request number to fetch the view and checks for. |

#### Scenario: Valid call
- **WHEN** `received_review_prepare` is called with `pr: 42`
- **THEN** the tool fetches the view and checks of PR 42

### Requirement: Config-version gate
The tool SHALL fail with a `DataError` whose message starts with `config-version:` when `.sdlc-v2/config.json` exists in the main worktree root but `.sdlc-v2/config.toml` does not.

- The gate runs on every call; there is no input field to skip it.
- The suggestion says to run `/setup`, then retry with the same `pr`.

#### Scenario: JSON-era project
- **WHEN** the main worktree root has `.sdlc-v2/` and no `.sdlc-v2/config.toml`
- **THEN** the call fails with a `DataError` starting with `config-version:`
- **AND** no `gh` command runs

### Requirement: PR number validation
The tool SHALL fail with a `DomainError` `pr must be a positive integer` when `pr` is `0` or negative.

#### Scenario: Missing pr
- **WHEN** the tool is called with `pr: 0`
- **THEN** the call fails with a `DomainError` `pr must be a positive integer`

### Requirement: Repository detection
The tool SHALL read the `origin` URL with `git remote get-url origin` in the active worktree and SHALL parse `owner` and `repo` from an HTTPS, `git@host:owner/repo`, or `ssh://` URL.

#### Scenario: No origin remote
- **WHEN** the active worktree has no remote named `origin`
- **THEN** the call fails with an `InfraError` starting with `get git remote URL:`

#### Scenario: Unparsable remote
- **WHEN** the `origin` URL has no owner/repo path
- **THEN** the call fails with an `InfraError` starting with `parse remote owner/repo:`

### Requirement: PR view and checks
The tool SHALL run `gh pr view <pr>` and `gh pr checks <pr>` in the active worktree and return their text output.

- A `gh pr view` failure fails the call.
- `gh pr checks` is best effort: its failure never fails the call.
- `gh pr checks` exits `1` when a check failed and `8` when a check is pending. Both are normal results, not failures.

| `gh pr checks` result | `checks` | `warnings` |
|---|---|---|
| Exit `0` | stdout | absent |
| Exit `1` or `8` with non-empty stdout | stdout | absent |
| Exit `1` or `8` with empty stdout (gh's own error, e.g. PR not found, auth, no checks reported) | empty | `gh pr checks <pr>: exit <N>: <stderr>` |
| Any other exit code | empty | `gh pr checks <pr>: exit <N>: <stderr>` |
| gh cannot be run (e.g. not on `PATH`) | empty | `gh pr checks <pr>: <error>` |

#### Scenario: PR does not exist
- **WHEN** `gh pr view 9999` fails
- **THEN** the call fails with an `InfraError` starting with `gh pr view 9999:`
- **AND** the suggestion says to confirm the PR number and `gh auth status`

#### Scenario: A check is failing
- **WHEN** `gh pr view 42` succeeds and `gh pr checks 42` prints `lint\tfail\t30s\thttps://x` and exits `1`
- **THEN** the call succeeds and `checks` is `lint\tfail\t30s\thttps://x`
- **AND** `warnings` is absent

#### Scenario: A check is pending
- **WHEN** `gh pr view 42` succeeds and `gh pr checks 42` prints `build\tpending\t1m\thttps://x` and exits `8`
- **THEN** the call succeeds and `checks` is `build\tpending\t1m\thttps://x`

#### Scenario: gh pr checks fails without rows
- **WHEN** `gh pr view 42` succeeds and `gh pr checks 42` prints nothing, writes `no checks reported on the 'feat' branch` to stderr, and exits `1`
- **THEN** the call succeeds with `checks` as an empty string
- **AND** `warnings` is `["gh pr checks 42: exit 1: no checks reported on the 'feat' branch"]`

### Requirement: Output fields
The tool SHALL return the fields below and SHALL NOT return review comments, thread status, or reply metadata.

| Field | Meaning |
|---|---|
| `version` | Always `1`. |
| `timestamp` | Call time, RFC3339, UTC. |
| `pr.number` | The input `pr`. |
| `pr.owner` | Owner parsed from `origin`. |
| `pr.repo` | Repository parsed from `origin`. |
| `view` | Trimmed stdout of `gh pr view <pr>`. |
| `checks` | Trimmed stdout of `gh pr checks <pr>`, or empty when gh failed. |
| `plugin_version` | Plugin version of the running binary. |
| `warnings` | Present only when `gh pr checks` failed: one message with gh's exit code and stderr. |

#### Scenario: Successful call
- **WHEN** `origin` is `git@github.com:owner/repo.git` and both `gh` commands succeed for `pr: 7`
- **THEN** `pr` is `{number: 7, owner: "owner", repo: "repo"}`
- **AND** `view` and `checks` hold the `gh` output

### Requirement: Error cases
The tool SHALL report each failure below with the listed error class.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root cannot be resolved | `InfraError` | `resolve project root: ...` / run inside a git repository or worktree |
| `.sdlc-v2/config.json` without `config.toml` | `DataError` | `config-version: ...` / run `/setup` |
| `pr` is `0` or negative | `DomainError` | `pr must be a positive integer` / look up the PR number |
| `git remote get-url origin` fails | `InfraError` | `get git remote URL: ...` / add an `origin` remote |
| `origin` URL cannot be parsed | `InfraError` | `parse remote owner/repo: ...` / use an owner/repo URL |
| `gh pr view <pr>` fails | `InfraError` | `gh pr view <pr>: ...` / confirm PR number and `gh auth status` |

#### Scenario: Active worktree cannot be resolved
- **WHEN** the active worktree root cannot be resolved but the main root can
- **THEN** the tool runs git and `gh` commands in the main root
