# Spec Delta

## MODIFIED Requirements

### Requirement: Input field
The tool SHALL accept one optional input field, `pr`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `pr` | int | no | JSON number, e.g. `42` | Pull request number to fetch the view and checks for. Omit it, or pass `0`, when no PR exists yet. |

#### Scenario: Valid call
- **WHEN** `received_review_prepare` is called with `pr: 42`
- **THEN** the tool fetches the view and checks of PR 42

#### Scenario: No PR number
- **WHEN** `received_review_prepare` is called with `{}`
- **THEN** the tool runs no `git` or `gh` command
- **AND** the call succeeds

#### Scenario: PR number zero
- **WHEN** `received_review_prepare` is called with `pr: 0`
- **THEN** the result is the same as for a call with no `pr`

### Requirement: PR number validation
The tool SHALL fail with a `DomainError` `pr must be a positive integer, or 0 to skip the PR overview` when `pr` is negative.

- The suggestion is `Pass the PR number for this branch (gh pr view), or omit pr when no PR exists yet, then retry received_review_prepare.`
- `pr` omitted or `0` is not an error.

#### Scenario: Negative pr
- **WHEN** the tool is called with `pr: -3`
- **THEN** the call fails with a `DomainError` `pr must be a positive integer, or 0 to skip the PR overview`
- **AND** no `git` or `gh` command runs

#### Scenario: Missing pr
- **WHEN** the tool is called with `pr: 0`
- **THEN** the call succeeds
- **AND** `view` is an empty string

### Requirement: Repository detection
When `pr` is greater than 0, the tool SHALL read the `origin` URL with `git remote get-url origin` in the active worktree and SHALL parse `owner` and `repo` from an HTTPS, `git@host:owner/repo`, or `ssh://` URL.

- With `pr` omitted or `0`, the tool reads no remote.

#### Scenario: No origin remote
- **WHEN** `pr` is `42`
- **AND** the active worktree has no remote named `origin`
- **THEN** the call fails with an `InfraError` starting with `get git remote URL:`

#### Scenario: Unparsable remote
- **WHEN** `pr` is `42`
- **AND** the `origin` URL has no owner/repo path
- **THEN** the call fails with an `InfraError` starting with `parse remote owner/repo:`

#### Scenario: No remote needed without a PR
- **WHEN** the active worktree has no remote named `origin`
- **AND** the call has no `pr`
- **THEN** the call succeeds with `pr.owner` and `pr.repo` as empty strings

### Requirement: PR view and checks
When `pr` is greater than 0, the tool SHALL run `gh pr view <pr>` and `gh pr checks <pr>` in the active worktree and return their text output.

- A `gh pr view` failure fails the call.
- `gh pr checks` is best effort: its failure never fails the call.
- `gh pr checks` exits `1` when a check failed and `8` when a check is pending. Both are normal results, not failures.
- With `pr` omitted or `0`, the tool runs no `gh` command and returns `view` and `checks` as empty strings.

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

#### Scenario: No gh call without a PR
- **WHEN** the call has no `pr`
- **THEN** `view` and `checks` are empty strings
- **AND** `warnings` has no `gh pr checks` entry

### Requirement: Output fields
The tool SHALL return the fields below and SHALL NOT return review comments, thread status, or reply metadata.

| Field | Meaning |
|---|---|
| `version` | Always `1`. |
| `timestamp` | Call time, RFC3339, UTC. |
| `pr.number` | The input `pr`, or `0` with no PR number. |
| `pr.owner` | Owner parsed from `origin`; `""` with no PR number. |
| `pr.repo` | Repository parsed from `origin`; `""` with no PR number. |
| `view` | Trimmed stdout of `gh pr view <pr>`; `""` with no PR number. |
| `checks` | Trimmed stdout of `gh pr checks <pr>`, or empty when gh failed or with no PR number. |
| `plugin_version` | Plugin version of the running binary. |
| `alwaysFixSeverities` | The checked list of auto-apply severities (see "Auto-apply severities"); always present; `(none)` when empty. |
| `warnings` | Present only when `gh pr checks` failed, or when the severity setting has a bad or repeated value: one message per problem. |
| `next` | The next step for the skill (see "Next text"). |

#### Scenario: Successful call
- **WHEN** `origin` is `git@github.com:owner/repo.git` and both `gh` commands succeed for `pr: 7`
- **THEN** `pr` is `{number: 7, owner: "owner", repo: "repo"}`
- **AND** `view` and `checks` hold the `gh` output
- **AND** `alwaysFixSeverities` is present

#### Scenario: No-PR call
- **WHEN** the call is `received_review_prepare({})`
- **THEN** `pr` is `{number: 0, owner: "", repo: ""}`
- **AND** `view` and `checks` are empty strings
- **AND** `alwaysFixSeverities` and `next` are present

### Requirement: Error cases
The tool SHALL report each failure below with the listed error class.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root cannot be resolved | `InfraError` | `resolve project root: ...` / run inside a git repository or worktree |
| `.sdlc-v2/config.json` without `config.toml` | `DataError` | `config-version: ...` / run `/setup` |
| `pr` is negative | `DomainError` | `pr must be a positive integer, or 0 to skip the PR overview` / pass the PR number, or omit `pr` when no PR exists yet |
| A local file cannot be read or parsed | `InfraError` | `read [receivedReview] section of .sdlc-v2/local.toml (or ~/.sdlc/local.toml): <cause>` / fix the TOML syntax or the permissions of the named file |
| `git remote get-url origin` fails (`pr` greater than 0) | `InfraError` | `get git remote URL: ...` / add an `origin` remote |
| `origin` URL cannot be parsed (`pr` greater than 0) | `InfraError` | `parse remote owner/repo: ...` / use an owner/repo URL |
| `gh pr view <pr>` fails | `InfraError` | `gh pr view <pr>: ...` / confirm PR number and `gh auth status` |

#### Scenario: Active worktree cannot be resolved
- **WHEN** the active worktree root cannot be resolved but the main root can
- **THEN** the tool runs git and `gh` commands in the main root

#### Scenario: Unreadable local file before the PR checks
- **WHEN** `.sdlc-v2/local.toml` does not parse
- **AND** the call passes `pr: 42`
- **THEN** the call fails with an `InfraError` starting with `read [receivedReview] section of`
- **AND** no `gh` command runs

## ADDED Requirements

### Requirement: Auto-apply severities
The tool SHALL return `alwaysFixSeverities` on every successful call: the lowercase items of `receivedReview.alwaysFixSeverities` from `~/.sdlc/local.toml` merged with `.sdlc-v2/local.toml`, where the project file wins.

- Allowed items: `critical`, `high`, `medium`, `low`, `info`.
- No file, no `[receivedReview]` section, or no key gives `[]`.
- The field is never absent. An empty list renders as `(none)`.

#### Scenario: Key absent
- **WHEN** `.sdlc-v2/local.toml` has a `[receivedReview]` section with no `alwaysFixSeverities` key
- **THEN** `alwaysFixSeverities` is `[]`
- **AND** `warnings` is absent

#### Scenario: Empty list
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = []`
- **THEN** `alwaysFixSeverities` is `[]`

#### Scenario: Listed values
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = ["high", "info"]`
- **THEN** `alwaysFixSeverities` is `["high", "info"]`

#### Scenario: Letter case
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = ["High"]`
- **THEN** `alwaysFixSeverities` is `["high"]`
- **AND** `warnings` is absent

#### Scenario: User-level file only
- **WHEN** only `~/.sdlc/local.toml` sets `alwaysFixSeverities = ["high"]`
- **THEN** `alwaysFixSeverities` is `["high"]`

#### Scenario: Project file wins
- **WHEN** `~/.sdlc/local.toml` sets `alwaysFixSeverities = ["high"]`
- **AND** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = ["low"]`
- **THEN** `alwaysFixSeverities` is `["low"]`

#### Scenario: Severities without a PR
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = ["critical"]`
- **AND** the call has no `pr`
- **THEN** `alwaysFixSeverities` is `["critical"]`

### Requirement: Bad severity values
The tool SHALL return `alwaysFixSeverities` as `[]` with one warning when the stored value has the wrong type or an unknown item, and SHALL keep a repeated item once with one warning. A bad value SHALL NOT fail the call.

#### Scenario: String instead of a list
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = "high"`
- **THEN** `alwaysFixSeverities` is `[]`
- **AND** `warnings` contains `local receivedReview.alwaysFixSeverities has wrong type string; expected a list of strings; using [] (every will-fix finding asks)`

#### Scenario: Item of the wrong type
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = ["high", 3]`
- **THEN** `alwaysFixSeverities` is `[]`
- **AND** `warnings` contains `local receivedReview.alwaysFixSeverities item 3 has wrong type int64; expected a string; using [] (every will-fix finding asks)`

#### Scenario: Unknown item
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = ["urgent"]`
- **THEN** `alwaysFixSeverities` is `[]`
- **AND** `warnings` contains `local receivedReview.alwaysFixSeverities "urgent" is not in allowed values [critical high medium low info]; using [] (every will-fix finding asks)`

#### Scenario: Repeated item
- **WHEN** `.sdlc-v2/local.toml` sets `alwaysFixSeverities = ["High", "high"]`
- **THEN** `alwaysFixSeverities` is `["high"]`
- **AND** `warnings` contains `local receivedReview.alwaysFixSeverities lists "high" more than once; kept once`

### Requirement: Unreadable local file
The tool SHALL fail with an `InfraError` when `~/.sdlc/local.toml` or `.sdlc-v2/local.toml` cannot be read or parsed, on a PR run and on a no-PR run.

- Message: `read [receivedReview] section of .sdlc-v2/local.toml (or ~/.sdlc/local.toml): <cause>`. The `<cause>` names the file.
- `review_prepare` returns the same error class for the same file.

#### Scenario: Malformed project file without a PR
- **WHEN** `.sdlc-v2/local.toml` does not parse
- **AND** the call has `pr: 0`
- **THEN** the call fails with an `InfraError` starting with `read [receivedReview] section of .sdlc-v2/local.toml (or ~/.sdlc/local.toml):`
- **AND** the suggestion is `Fix the TOML syntax or the file permissions of the file the error names (or delete its receivedReview section to ask for every finding), then retry received_review_prepare.`

#### Scenario: Malformed user-level file
- **WHEN** `~/.sdlc/local.toml` does not parse
- **THEN** the call fails with an `InfraError` whose message names `~/.sdlc/local.toml` or its resolved path

### Requirement: Next text
The tool SHALL set `next` by run kind.

| Case | `next` |
|---|---|
| `pr` greater than 0 | `PR overview loaded. Fetch the review comments in Step 1b. Use alwaysFixSeverities for the Step 10 pre-approval.` |
| `pr` omitted or `0` | `No PR number: PR overview and checks skipped. Take the feedback from the dispatch prompt, the conversation or a user paste in Step 1b. Use alwaysFixSeverities for the Step 10 pre-approval.` |

#### Scenario: PR run next
- **WHEN** the call with `pr: 42` succeeds
- **THEN** `next` is `PR overview loaded. Fetch the review comments in Step 1b. Use alwaysFixSeverities for the Step 10 pre-approval.`

#### Scenario: No-PR run next
- **WHEN** the call with no `pr` succeeds
- **THEN** `next` starts with `No PR number: PR overview and checks skipped.`
