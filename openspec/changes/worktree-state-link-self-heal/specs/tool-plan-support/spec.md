# Spec Delta

## MODIFIED Requirements

### Requirement: Preplan context
The `preplan_context` action SHALL return the plan guardrails and the topic file path, SHALL create the topic file with a fixed skeleton only when it is absent, and SHALL NOT start a plan run.

- The file name is the slug of the topic: lowercase ASCII letters, digits and `-`.
- A topic is 1-50 characters on one line, with at least one ASCII letter or digit.
- Skeleton sections, in order: `## Goal`, `## Users and effect`, `## Flows`, `## Decisions`, `## Open questions`, `## Guardrail check`.
- A symlink whose target does not exist, at `.sdlc-v2/preplan` or at the topic file, returns an `InfraError` that names the link and the target. Nothing is written.

#### Scenario: New topic
- **WHEN** `topic` is `auth flow` and `<main-worktree>/.sdlc-v2/preplan/auth-flow.md` does not exist
- **THEN** the file exists with the skeleton text
- **AND** `## Flows` comes right after `## Users and effect`
- **AND** `preplanCreated` is `true`

#### Scenario: Existing topic
- **WHEN** the topic file already exists
- **THEN** the file content does not change
- **AND** `preplanCreated` is `false`

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
