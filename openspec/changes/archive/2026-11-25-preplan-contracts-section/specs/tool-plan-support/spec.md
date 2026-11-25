# Spec Delta

## MODIFIED Requirements

### Requirement: Preplan context
The `preplan_context` action SHALL return the plan guardrails and the topic file path, SHALL create the topic file with the skeleton only when no topic file of the slug exists, and SHALL NOT start a plan run.

- Skeleton: the title, `**Status:**` and `**Started:**` lines, then `## Goal`, `## Problem and benefit`, `## Users and effect`, `## Risk if wrong`, `## Non-goals`, `## Flows`, `## Contracts`, `## Decisions` (table header only).

#### Scenario: New topic
- **WHEN** `topic` is `auth flow`, the UTC date is `2026-10-12`, and `<main-worktree>/.sdlc-v2/preplan/` holds no topic file of the slug `auth-flow`
- **THEN** `<main-worktree>/.sdlc-v2/preplan/2026-10-12-auth-flow.md` exists with the skeleton text
- **AND** the file starts with the line `# Preplan: auth flow`, then the line `**Status:** in progress`
- **AND** the file holds a `**Started:**` line with the UTC time of the call (RFC 3339), after the `**Status:**` line
- **AND** the file holds the 8 sections in skeleton order, and `## Contracts` comes right after `## Flows` and right before `## Decisions`
- **AND** the file has no `## Open questions` section and no `## Guardrail check` section
- **AND** the file ends after the `## Decisions` table header
- **AND** `preplanCreated` is `true`
- **AND** the summary says `Created topic file 2026-10-12-auth-flow.md.`

#### Scenario: Existing topic
- **WHEN** the folder holds `auth-flow.md` or `2026-10-01-auth-flow.md` with all 8 skeleton headings
- **THEN** the tool returns that file
- **AND** the file content does not change
- **AND** `preplanCreated` is `false`
- **AND** `next` is `Read preplanFile, then continue with its first gap: the first empty section, then the open questions, if any.`

#### Scenario: Name that does not match
- **WHEN** the folder holds only `other-auth-flow.md`, `2026-1-12-auth-flow.md` and `auth-flow.md.bak`
- **THEN** the tool creates a new dated topic file

#### Scenario: Two matching files
- **WHEN** the folder holds `auth-flow.md` and `2026-10-01-auth-flow.md`
- **THEN** the tool returns a `DataError` that names both files
- **AND** the Suggestion is `Rename or delete one of: 2026-10-01-auth-flow.md, auth-flow.md. Then run /sdlc:preplan again.`
- **AND** no file is written

#### Scenario: Folder that cannot be listed
- **WHEN** a regular file is at the `.sdlc-v2/preplan` path
- **THEN** the tool returns an `InfraError` whose message starts with `preplan_context: list: `
- **AND** the Suggestion starts with `Make <dir> a folder that you can read`
- **AND** no file is written

#### Scenario: Folder over the bound
- **WHEN** `.sdlc-v2/preplan` holds more than 10000 names
- **THEN** the tool returns an `InfraError` with the Suggestion `Move old topic files out of <dir>. Then run /sdlc:preplan again.`
- **AND** no file is written

#### Scenario: Missing folder
- **WHEN** `.sdlc-v2/preplan` does not exist
- **THEN** the tool creates the folder and a dated topic file

#### Scenario: Bad topic
- **WHEN** `topic` is `!!!`
- **THEN** the tool returns a `DomainError` with a Suggestion
- **AND** no file is written

#### Scenario: No guardrails configured
- **WHEN** the config has no plan guardrails
- **THEN** the summary says `0 guardrail(s) loaded — none configured.`
- **AND** `next` is not empty

#### Scenario: Guardrails unavailable
- **WHEN** the plan config cannot be read
- **THEN** `next` ends with `preplan_check writes the warning into the Guardrail check line at Step 4.`

#### Scenario: Dangling preplan folder link
- **WHEN** `.sdlc-v2/preplan` is a symlink to `<target>`, which does not exist
- **THEN** the tool returns an `InfraError` whose message names `.sdlc-v2/preplan` and `<target>`
- **AND** the Suggestion is `Start a new session so the session-start hook repairs the link, or run: mkdir -p <target>`
- **AND** `<target>` is not created
- **AND** nothing is written

#### Scenario: Dangling topic file link
- **WHEN** the topic file path is a symlink whose target does not exist
- **THEN** the tool returns an `InfraError` that names the link and its target, with the Suggestion `Remove the link, then try again: rm <link>`
- **AND** the result is not `preplanCreated: false`
- **AND** nothing is written

## ADDED Requirements

### Requirement: Preplan topic file name
The `preplan_context` action SHALL use the one file in `.sdlc-v2/preplan/` named exactly `<slug>.md` or `YYYY-MM-DD-<slug>.md` (4, 2 and 2 digits) as the topic file, and SHALL name a new file `YYYY-MM-DD-<slug>.md` with the UTC date of the call.

- The slug of the topic: lowercase ASCII letters, digits and `-`.
- A topic is 1-50 characters on one line, with at least one ASCII letter or digit.
- The tool reads at most 10000 names of the folder.

#### Scenario: Undated file name
- **WHEN** the only topic file of the slug `auth-flow` is `auth-flow.md`
- **THEN** `preplan_context` returns `auth-flow.md` as `preplanFile`
- **AND** no dated file is created

#### Scenario: Topic with two words
- **WHEN** `topic` is `Auth Flow`, the UTC date is `2026-10-12` and no topic file of the slug exists
- **THEN** the new file is `2026-10-12-auth-flow.md`

#### Scenario: Topic too long
- **WHEN** `topic` has 51 characters after the outer spaces are trimmed
- **THEN** the tool returns a `DomainError` with a Suggestion
- **AND** no file is written

### Requirement: Preplan heading repair
The `preplan_context` action SHALL add each absent skeleton heading of an existing topic file after its nearest earlier section, SHALL return the added headings in `addedSections` in skeleton order, and SHALL write nothing when every heading is there.

#### Scenario: Older topic file
- **WHEN** the topic file has only the sections `## Goal`, `## Users and effect`, `## Flows` and `## Decisions`
- **THEN** the tool adds `## Problem and benefit`, `## Risk if wrong`, `## Non-goals` and `## Contracts`
- **AND** `## Contracts` comes right after `## Flows` and right before `## Decisions`
- **AND** `addedSections` lists `## Problem and benefit`, `## Risk if wrong`, `## Non-goals`, `## Contracts`
- **AND** the summary holds `Added 4 section heading(s): ## Problem and benefit, ## Risk if wrong, ## Non-goals, ## Contracts.`
- **AND** `next` starts with `Read preplanFile: preplan_context added 4 section heading(s).`

#### Scenario: Older topic file with no Flows section
- **WHEN** the topic file has each skeleton section except `## Flows` and `## Contracts`
- **THEN** the file has `## Flows` right after `## Non-goals`, then `## Contracts`, then `## Decisions`

#### Scenario: Every heading present
- **WHEN** the topic file has all 8 skeleton headings
- **THEN** the file bytes do not change
- **AND** `addedSections` is `(none)`

#### Scenario: Open fence at the end of an older file
- **WHEN** a code fence is still open at the end of a topic file that lacks a skeleton heading
- **THEN** the tool adds a closing fence line at the end of the file before the added headings
- **AND** the summary names that closing fence line

### Requirement: Preplan Contracts format rule
The `preplan_write` action SHALL refuse a `Contracts` body that breaks rule P12 with a `DomainError` that names `P12`, and SHALL write nothing.

- P12 form: each line outside a code fence is a label line `**#<id>[, #<id>] <Kind>: <name>**` (Kind is `Endpoint`, `Data`, `Config` or `Message`), a line of the code fence or table right below a label, or the one line `No contract: #<id>, #<id>.` with ascending ids.
- P12 reads only the Contracts body, not the `## Decisions` table.

#### Scenario: Valid Contracts body
- **WHEN** the `Contracts` body is the line `**#2 Endpoint: POST /api/topics**`, an `http` code fence that closes, a blank line and the line `No contract: #1, #5.`
- **THEN** the tool replaces the full `## Contracts` body with that body

#### Scenario: Contracts ids that are not in Decisions
- **WHEN** the `Contracts` body has the label line `**#9 Data: topics table**` and a table, and `## Decisions` has no row 9
- **THEN** the tool writes the body

#### Scenario: Blank Contracts body
- **WHEN** `section` is `Contracts` and `content` is an empty string
- **THEN** the tool returns the `DomainError` `Section "Contracts" breaks rule P12: the body is blank: send a labelled block or a No contract line`
- **AND** the file bytes are unchanged

#### Scenario: Prose line in Contracts
- **WHEN** the `Contracts` body is the line `The endpoint takes a name.`
- **THEN** the tool returns the `DomainError` `Section "Contracts" breaks rule P12: line 1 is not a label line, a code fence, a table row or the No contract line: "The endpoint takes a name.". A label line is **#<id>[, #<id>] <Kind>: <name>**, and Kind is one of Endpoint, Data, Config, Message`

#### Scenario: Old no-contract line
- **WHEN** the `Contracts` body is the line `No contract change.`
- **THEN** the tool returns a `DomainError` that names rule `P12` and line 1

#### Scenario: Label with no block
- **WHEN** the `Contracts` body is `**#2 Endpoint: POST /x**`, then `**#3 Data: t**` and a table
- **THEN** the tool returns the `DomainError` `Section "Contracts" breaks rule P12: line 1 is a label line with no code fence or table below it: put a code fence or a table right below each label line`

#### Scenario: Unknown kind
- **WHEN** the `Contracts` body is `**#2 Api: x**` and a code fence
- **THEN** the tool returns a `DomainError` that names rule `P12` and line 1

#### Scenario: Id in a label and in the no-contract line
- **WHEN** the `Contracts` body is `**#2 Endpoint: POST /x**`, a code fence and `No contract: #2.`
- **THEN** the tool returns the `DomainError` `Section "Contracts" breaks rule P12: #2 is in a label line and in the No contract line: keep each id in one place only`

#### Scenario: Two blocks with one kind and name
- **WHEN** the `Contracts` body has `**#1 Data: Topics**` with a table and `**#2 Data: topics**` with a table
- **THEN** the tool returns a `DomainError` that names rule `P12`

#### Scenario: No-contract ids out of order
- **WHEN** the `Contracts` body is the line `No contract: #5, #2.`
- **THEN** the tool returns a `DomainError` that names rule `P12`

#### Scenario: Repeated no-contract id
- **WHEN** the `Contracts` body is the line `No contract: #2, #2.`
- **THEN** the tool returns the `DomainError` `Section "Contracts" breaks rule P12: line 1 (the No contract line) lists #2 twice: list each id once`

#### Scenario: P12 Suggestion
- **WHEN** the tool refuses a `Contracts` body with rule `P12`
- **THEN** the Suggestion names the label form `**#<id>[, #<id>] <Kind>: <name>**` and each kind: `Endpoint, Data, Config, Message`

#### Scenario: Open fence in Contracts
- **WHEN** the `Contracts` body is a label line and a code fence that does not close
- **THEN** the tool returns a `DomainError` that names rule `P3`, not `P12`

### Requirement: Preplan check contract gaps
The `preplan_check` action SHALL return `contractMissing` and `contractStale`, read from the live `## Decisions` rows and the contract answers in `## Contracts`, each in ascending order, and SHALL render an empty list as `(none)`.

- A live row has a Decision cell that starts with neither `Dropped:` nor `Moved:`.
- A contract answer is a label line outside a code fence that lists `#<n>`, or `#<n>` in the `No contract:` line.
- Other actions return neither field.

#### Scenario: Skeleton file
- **WHEN** the topic file has no `## Decisions` row and a blank `## Contracts` body
- **THEN** the output holds `- contractMissing: (none)` and `- contractStale: (none)`

#### Scenario: Live rows with no answer
- **WHEN** `## Decisions` has the live rows 1 and 2 and `## Contracts` is blank
- **THEN** the output holds the line `- contractMissing:` followed by the lines `  - #1` and `  - #2`
- **AND** `contractStale` is `(none)`
- **AND** `next` holds `contractMissing lists 2 live proposal(s) with no contract answer: #1, #2.`

#### Scenario: Each live row answered
- **WHEN** `## Decisions` has the live rows 1 and 2, and `## Contracts` holds `**#1 Data: topics table**` with a table and the line `No contract: #2.`
- **THEN** `contractMissing` is `(none)`
- **AND** `contractStale` is `(none)`

#### Scenario: Dropped and moved rows
- **WHEN** row 1 is live, row 2 starts with `Dropped:`, row 3 starts with `Moved:`, and `## Contracts` holds `**#1, #2 Data: topics table**` with a table and `No contract: #3.`
- **THEN** `contractMissing` is `(none)`
- **AND** `contractStale` lists `#2` and `#3`
- **AND** `next` holds `contractStale lists 2 id(s) that are not live: #2, #3. Readers skip them. The next ## Contracts write that keeps another block removes them.`

#### Scenario: No Contracts heading
- **WHEN** row 1 is live and the topic file has no `## Contracts` heading
- **THEN** `contractMissing` lists `#1`

#### Scenario: Label that is not valid
- **WHEN** row 1 is live and the only text that names `#1` in `## Contracts` is `**#1 Api: x**` or a line inside a code fence
- **THEN** `contractMissing` lists `#1`
- **AND** `next` holds `## Contracts breaks a format rule` and the P12 error of the body

#### Scenario: Decisions table that breaks P4 or P5
- **WHEN** `## Decisions` has two rows with `#` value `1`, or a row with 2 cells, or a damaged header line
- **THEN** `contractMissing` and `contractStale` are `(none)`
- **AND** `contractChecked` is false
- **AND** `next` holds `The contract check did not run: the ## Decisions table breaks rule P4 or P5.`

### Requirement: Preplan check contract status
The `preplan_check` action SHALL return `contractChecked`: true when the contract check ran, and false when a code fence is still open at the end of the file, when the file has no `## Decisions` heading, or when the `## Decisions` table breaks rule P4 or P5.

- When `contractChecked` is false, `contractMissing` and `contractStale` are `(none)`, and `summary` and `next` name the cause.
- Other actions do not return the field.

#### Scenario: Open fence hides Contracts and Decisions
- **WHEN** a code fence that opens in `## Flows` is still open at the end of the file
- **THEN** `contractChecked` is false and both lists are `(none)`
- **AND** `next` holds `The contract check did not run: a code fence is still open, so the headings after it count as absent.`

#### Scenario: No Decisions heading
- **WHEN** the topic file has no `## Decisions` heading and `## Contracts` lists `#1`
- **THEN** `contractChecked` is false and `contractStale` is `(none)`
- **AND** `next` holds `The contract check did not run: the file has no ## Decisions heading.`

#### Scenario: Other action
- **WHEN** `action` is `preplan_context`
- **THEN** the output has no `contractMissing` field and no `contractStale` field
