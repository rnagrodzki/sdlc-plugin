# Spec Delta

## MODIFIED Requirements

### Requirement: Preplan context
The `preplan_context` action SHALL return the plan guardrails and the topic file path, SHALL create the topic file with a fixed skeleton only when no topic file of the slug exists, and SHALL NOT start a plan run.

- The slug of the topic: lowercase ASCII letters, digits and `-`.
- A topic is 1-50 characters on one line, with at least one ASCII letter or digit.
- The topic file of a slug is the one file in `.sdlc-v2/preplan/` named exactly `<slug>.md` or `YYYY-MM-DD-<slug>.md` (4, 2 and 2 digits). A new file is `YYYY-MM-DD-<slug>.md`, with the UTC date of the call.
- The tool reads at most 10000 names of the folder.
- Skeleton: the `# Preplan: <topic>` heading, the `**Status:** in progress` line, the `**Started:** <UTC RFC 3339 time>` line, then the sections `## Goal`, `## Users and effect`, `## Flows`, `## Decisions`, `## Open questions`, `## Guardrail check`.
- A symlink whose target does not exist, at `.sdlc-v2/preplan` or at the topic file, returns an `InfraError` that names the link and the target. Nothing is written.

#### Scenario: New topic
- **WHEN** `topic` is `auth flow`, the UTC date is `2026-10-12`, and `<main-worktree>/.sdlc-v2/preplan/` holds no topic file of the slug `auth-flow`
- **THEN** `<main-worktree>/.sdlc-v2/preplan/2026-10-12-auth-flow.md` exists with the skeleton text
- **AND** the file holds a `**Started:**` line with the UTC time of the call, after the `**Status:**` line
- **AND** `## Flows` comes right after `## Users and effect`
- **AND** `preplanCreated` is `true`
- **AND** the summary says `Created topic file 2026-10-12-auth-flow.md.`

#### Scenario: Existing topic
- **WHEN** the folder holds `auth-flow.md` or `2026-10-01-auth-flow.md`
- **THEN** the tool returns that file
- **AND** the file content does not change
- **AND** `preplanCreated` is `false`

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
- **THEN** the tool returns an `InfraError` whose message starts with `preplan_context: list `
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

#### Scenario: Dangling preplan folder link
- **WHEN** `.sdlc-v2/preplan` is a symlink to `<target>`, which does not exist
- **THEN** the tool returns an `InfraError` whose message names `.sdlc-v2/preplan` and `<target>`
- **AND** the Suggestion is `Start a new session so the session-start hook repairs the link, or run: mkdir -p <target>`
- **AND** `<target>` is not created

#### Scenario: Dangling topic file link
- **WHEN** the topic file path is a symlink whose target does not exist
- **THEN** the tool returns an `InfraError` with the Suggestion `Remove the link, then try again: rm <link>`
- **AND** the result is not `preplanCreated: false`
