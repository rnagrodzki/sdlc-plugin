## ADDED Requirements

### Requirement: Review body and thread state reads
In classify mode, after the comment fetch, the tool SHALL read the review bodies with `gh pr view <pr> --json reviews` and the thread state with one paginated `gh api graphql` call. A failure of either read SHALL add a `warnings` line, not an error.

- A comment fetch failure still fails the call with an `InfraError`.
- `warnings` is `[]` when both reads succeed, and in write mode.

#### Scenario: Review read fails
- **WHEN** `gh pr view <pr> --json reviews` exits 1 with `HTTP 502`
- **THEN** the call succeeds and returns `threads`
- **AND** `warnings` holds one line that starts with `review body not read: HTTP 502`

#### Scenario: Thread state
- **WHEN** the GraphQL read reports the thread of root comment 4575025310 as resolved
- **THEN** that thread has `isResolved: true`

### Requirement: Read warning text
Each warning SHALL start with the name of the failed read and its cause, and SHALL name the fields that stay empty.

| Id | Starts with | Empty fields |
|---|---|---|
| W1 | `review body not read: <cause>;` | `severityIcon`, `overview`, `overviewFindings` |
| W2 | `thread state not read: <cause>;` | `isResolved`, `isOutdated` |

- W2 also says `Do not use isResolved or isOutdated to skip a thread.`

#### Scenario: Thread state read fails
- **WHEN** the GraphQL read exits 1
- **THEN** `warnings` holds one line that starts with `thread state not read:`
- **AND** that line holds `Do not use isResolved or isOutdated to skip a thread.`

### Requirement: Warning cause
The `<cause>` of a warning SHALL be the gh error text, `gh output exceeded the size cap`, or `gh call failed or gave output that could not be read`. It SHALL name no Go package or function.

#### Scenario: Output cap
- **WHEN** the review read stops at the gh output size cap
- **THEN** the W1 line starts with `review body not read: gh output exceeded the size cap;`

#### Scenario: No package names
- **WHEN** the review read exits 1 with `HTTP 404`
- **THEN** the W1 line holds neither `ghx:` nor `execx:`

### Requirement: Read warnings in next
When `warnings` is not empty, `next` SHALL end with ` Read warnings: some fields are empty.` one time.

#### Scenario: Both extra reads fail
- **WHEN** the review read and the GraphQL read both fail
- **THEN** `warnings` has 2 lines, W1 then W2
- **AND** `next` holds ` Read warnings: some fields are empty.` one time

### Requirement: Copilot comment severity
The tool SHALL give a thread by Copilot a `severity` that is the higher of its inline prefix and its overview icon, by the rank critical > high > medium > low. It SHALL keep both values in `severityPrefix` and `severityIcon`.

- A thread with neither value has no `severity` field.

#### Scenario: Prefix and icon differ
- **WHEN** a Copilot comment starts with `[low]` and its overview item has `alt="High severity"`
- **THEN** the thread has `severity: high`, `severityPrefix: low`, `severityIcon: high`

#### Scenario: No severity source
- **WHEN** a Copilot comment has no prefix and no overview item
- **THEN** the thread has no `severity` field

### Requirement: Inline severity prefix
The tool SHALL read a `[level]` prefix at the start of a Copilot root comment, in any letter case: `critical`, `high`, `medium`, `low`, or `minor`. `minor` SHALL give `low`. An unknown tag SHALL give no prefix. A comment by another reviewer SHALL get no prefix.

#### Scenario: Minor prefix
- **WHEN** a Copilot comment starts with `[Minor]` and has no overview item
- **THEN** the thread has `severity: low`

#### Scenario: Person types a prefix
- **WHEN** a comment by `alice` starts with `[high]`
- **THEN** the thread has no `severityPrefix` and no `severity`

### Requirement: Overview severity icon
The tool SHALL set `severityIcon` from the `alt="<Level> severity"` image of the comment's item in the latest Copilot review that has an overview header.

#### Scenario: Icon only
- **WHEN** a Copilot comment has no prefix and its overview item has `alt="Medium severity"`
- **THEN** the thread has `severity: medium` and `severityIcon: medium`

### Requirement: Copilot overview fields
The tool SHALL return `overview` and `overviewFindings` from the latest Copilot review that has an overview header.

- `overview` is `approval-recommended`, `changes-recommended`, `needs-closer-look`, or `""` with no such review.
- `overviewFindings` has one `{title, path, line, severity, body}` entry for each previously missed item, and is `[]` when none.

#### Scenario: No Copilot overview
- **WHEN** no review body has an overview header
- **THEN** `overview` is `""` and `overviewFindings` is `[]`

### Requirement: Overview review choice
To find the overview, the tool SHALL skip `PENDING` reviews and reviews with no header, and SHALL take the newest Copilot review by `submittedAt`.

#### Scenario: Newer review with no header
- **WHEN** Copilot has a review with the header `### 🟡 Changes recommended` and a later review with no header
- **THEN** `overview` is `changes-recommended`

### Requirement: Thread overview section
The tool SHALL set `overviewSection` to `open` or `resolved` on each thread that the latest Copilot overview links with `#discussion_r<id>`, from the block that holds the link.

#### Scenario: Resolved block link
- **WHEN** the `2 resolved since last review` block links `#discussion_r4570000001`
- **THEN** the thread of root comment 4570000001 has `overviewSection: resolved`

### Requirement: Missed findings output
Paths in `overviewFindings` SHALL hold no zero-width space (U+200B). When `overviewFindings` has N > 0 entries, `next` SHALL add ` The latest Copilot review also lists N finding(s) with no thread (overviewFindings): fix or defer each; they take no reply.` The counts `outstanding`, `replied` and `total` SHALL not include `overviewFindings`.

#### Scenario: Missed finding
- **WHEN** the latest Copilot review lists `1 previously missed issue` at `src/​metadata.ts:646` with a low icon
- **THEN** `overviewFindings` is `[{title: "Stale metadata key", path: "src/metadata.ts", line: 646, severity: "low", body: "..."}]`
- **AND** `next` holds `lists 1 finding(s) with no thread`
