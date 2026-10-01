# tool-mcp-failure-record Specification

## Purpose
`mcp_failure_record` classifies one failed MCP tool call and appends a structured block to the project learnings log. It is internal: sdlc skills (for example `jira`) call it after an MCP call fails. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input fields
The tool SHALL accept the input fields below, with `tool` as the only required field.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `tool` | string | yes | plain text, e.g. `getJiraIssue` | Name of the MCP tool call that failed |
| `httpStatus` | int | no | number, e.g. `401` | HTTP status of the failed call |
| `errorMessage` | string | no | plain text | Error text; used for keyword classification and logged |
| `hookDenyReason` | string | no | plain text, e.g. `R20: blocked` | Deny reason from a hook that blocked the call |
| `rPath` | string | no | plain text, e.g. `R22` | Guardrail / rule code tied to the failure |
| `site` | string | no | plain text | Site id (e.g. Jira host), logged |
| `project` | string | no | plain text | Project id, logged |
| `recovered` | string | no | plain text, e.g. `no`, `yes:R9` | How the failure was recovered, logged |
| `sessionId` | string | no | plain text | Explicit session id override |

There is no input field for the class. The tool always derives it.

#### Scenario: Missing tool is rejected
- **WHEN** `mcp_failure_record` is called with `tool` empty
- **THEN** it returns a DomainError with message `mcp_failure_record: tool is required`
- **AND** nothing is written to `.sdlc-v2/learnings/log.md`

### Requirement: Failure classification
The tool SHALL map the failure signal to exactly one class from the closed set `transport`, `auth`, `schema`, `workflow`, `hook-block`, `link-verification`, `unknown`, using the first matching row below.

| Order | Condition | Class |
|---|---|---|
| 1 | `hookDenyReason` contains `R20` or `R21` | `hook-block` |
| 2 | `hookDenyReason` contains `R19`, `C13`, `R18`, `R25` or `G15` | `schema` |
| 3 | `rPath` equals `R22` exactly (no prefix match) | `link-verification` |
| 4 | `httpStatus` is 401, or `errorMessage` contains `cloudId`, `namespace` or `unauthorized` (case-insensitive) | `auth` |
| 5 | `httpStatus` is 403 | `auth` |
| 6 | `httpStatus` is 400 and `errorMessage` contains `transition`, `workflow` or `invalid status` (case-insensitive) | `workflow` |
| 7 | `httpStatus` is 400 | `schema` |
| 8 | `httpStatus` is 500 or higher, or `errorMessage` contains `ECONNREFUSED`, `ETIMEDOUT` or `fetch failed` (case-insensitive) | `transport` |
| 9 | none of the above | `unknown` |

#### Scenario: Hook block wins over a server error
- **WHEN** `hookDenyReason` is `R20: blocked` and `httpStatus` is 500
- **THEN** the returned `class` is `hook-block`

#### Scenario: Auth keyword wins over a workflow keyword
- **WHEN** `httpStatus` is 400 and `errorMessage` is `unauthorized transition`
- **THEN** the returned `class` is `auth`

#### Scenario: 400 without workflow keywords
- **WHEN** `httpStatus` is 400 and `errorMessage` is `bad request`
- **THEN** the returned `class` is `schema`

#### Scenario: rPath is matched exactly
- **WHEN** `rPath` is `R22x` and no other signal is set
- **THEN** the returned `class` is `unknown`

#### Scenario: Workflow keyword without a 400
- **WHEN** `errorMessage` is `workflow` and `httpStatus` is not set
- **THEN** the returned `class` is `unknown`

### Requirement: Log entry written to the main worktree
The tool SHALL append one block to `<main worktree root>/.sdlc-v2/learnings/log.md`, creating the file and its parent directories when missing.

Block layout (heading uses an em dash, then `mcp-failure[<class>]: <tool>`; it names no skill or service, because any skill can record a failure for any tool):

```text
## <YYYY-MM-DD UTC> — mcp-failure[<class>]: <tool>
tool: <tool>
site: <site>
project: <project>
error: <errorMessage, one line, max 300 bytes>
recovered: <recovered, default "no">
```

- Exactly one blank line separates the block from the content before it, so the block stays its own `learnings_log` entry. An empty file gets no leading blank line, and a file that already ends in a blank line gets no extra one.

#### Scenario: First failure creates the log
- **WHEN** `.sdlc-v2/learnings/` does not exist and the tool is called with `tool: "test_tool"`, `httpStatus: 401`, `errorMessage: "unauthorized access"`
- **THEN** `.sdlc-v2/learnings/log.md` is created
- **AND** it contains the heading `## <today UTC> — mcp-failure[auth]: test_tool`
- **AND** the heading does not contain `jira`

#### Scenario: Block after an existing entry
- **WHEN** the log holds one `learnings_log` entry ending in a single newline and the tool records a failure
- **THEN** one blank line separates that entry from the new heading
- **AND** `learnings_log` action `stats` reports `totalEntries` `2`

#### Scenario: Recovered defaults to no
- **WHEN** the tool is called without `recovered`
- **THEN** the block contains the line `recovered: no`

### Requirement: Same-day de-duplication
The tool SHALL skip the append when the log already contains the exact heading line for the same UTC date, class and tool.

- Two different error messages for the same tool and class on the same UTC day produce one block.
- The match is on the full heading line, so tool `foo` is not suppressed by an existing entry for tool `foobar`.
- A skipped append still returns `recorded: true`.

#### Scenario: Duplicate failure is a no-op
- **WHEN** the same failure (`tool: "test_tool"`, `httpStatus: 401`) is recorded twice on the same UTC day
- **THEN** the second call succeeds
- **AND** the size of `.sdlc-v2/learnings/log.md` does not change

#### Scenario: Tool name prefix does not collide
- **WHEN** a failure for tool `foobar` is recorded and then a failure with the same class for tool `foo`
- **THEN** the log contains both `mcp-failure[auth]: foobar` and `mcp-failure[auth]: foo`

### Requirement: Secret redaction and truncation
The tool SHALL redact secrets in `tool`, `site`, `project` and `errorMessage` before writing, and SHALL write `errorMessage` as one line of at most 300 bytes.

| Pattern | Written as |
|---|---|
| `Bearer <token>` | `Bearer [REDACTED]` |
| JWT (`eyJ...` three dot-separated parts) | `[jwt:REDACTED]` |
| `cookie:<value>` (case-insensitive) | `cookie:[REDACTED]` |
| `cloudId` / `cloud_id` followed by a 30+ character hex id | `cloudId=[REDACTED:<first 6 chars>…]` |
| Email address | `[email:REDACTED]` |

- Newlines in `errorMessage` are replaced by spaces before truncation.
- `recovered` is written as given, without redaction.

#### Scenario: Email is redacted
- **WHEN** `errorMessage` is `failed for user@example.com`
- **THEN** the log does not contain `user@example.com`
- **AND** it contains `[email:REDACTED]`

#### Scenario: Long error is truncated
- **WHEN** `errorMessage` is 500 characters long
- **THEN** the `error:` line value is at most 300 bytes

### Requirement: Session id resolution
The tool SHALL return a `sessionId` taken from the first non-empty source below; it SHALL NOT create any session marker file.

| Order | Source |
|---|---|
| 1 | `sessionId` input field |
| 2 | Session id of the calling MCP connection |
| 3 | Trimmed content of `<main worktree root>/.sdlc-v2/state/mcp-session.id` |
| 4 | `SDLC_SESSION_ID` environment variable |
| 5 | Parent process id of the MCP server |

#### Scenario: Explicit session id wins
- **WHEN** the tool is called with `sessionId: "abc"`
- **THEN** the output `sessionId` is `abc`

#### Scenario: No source available
- **WHEN** no input, connection session, marker file or `SDLC_SESSION_ID` value is available
- **THEN** the output `sessionId` is the parent process id as a decimal string

### Requirement: Output fields
The tool SHALL return the fields below on success.

| Field | Meaning |
|---|---|
| `class` | Derived failure class (see Failure classification) |
| `sessionId` | Resolved session id |
| `recorded` | Always `true` on success, including a de-duplicated no-op |

#### Scenario: Successful record
- **WHEN** the tool is called with `tool: "test_tool"`, `httpStatus: 401`, `errorMessage: "unauthorized access"`
- **THEN** the output has `class: auth`, `recorded: true` and a non-empty `sessionId`

### Requirement: Error cases
The tool SHALL report failures with the classes below.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `tool` is empty | DomainError | `mcp_failure_record: tool is required` / pass the `tool` field |
| Main worktree root cannot be resolved | InfraError | `resolve project root: <err>` / run inside a git worktree with a valid `.sdlc-v2` root |
| Log directory or file cannot be created or written | InfraError | `record mcp failure: <err>` / check write permission on `.sdlc-v2/learnings/log.md` |

#### Scenario: Log is not writable
- **WHEN** `.sdlc-v2/learnings/log.md` cannot be opened for append
- **THEN** the tool returns an InfraError whose message starts with `record mcp failure:`

### Requirement: Tool annotations
The tool SHALL register with title `Record MCP tool failure` and annotations `ReadOnly: true`, `Destructive: false`, `Idempotent: true`, `OpenWorld: false`.

- Its description marks it `INTERNAL — called by sdlc skills only.`
- It writes only to the gitignored `.sdlc-v2/learnings/` log.

#### Scenario: Registered annotations
- **WHEN** a client lists the server tools
- **THEN** `mcp_failure_record` reports `ReadOnly: true`, `Idempotent: true` and `OpenWorld: false`
