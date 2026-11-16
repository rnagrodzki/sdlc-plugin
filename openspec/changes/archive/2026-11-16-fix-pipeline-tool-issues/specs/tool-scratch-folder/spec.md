# Spec Delta

## Purpose

Defines where MCP tools keep short-lived files (manifests, poll state, validation copies) and how old files go away, so no tool writes to the system temp folder.

## ADDED Requirements

### Requirement: Tool short-lived files stay in the scratch folder
Every MCP tool SHALL write its short-lived files under `<main worktree>/.sdlc-v2/scratch/` and SHALL NOT write them to the system temp folder (`os.TempDir()`, `$TMPDIR`, `/tmp`, `/var/folders`).

#### Scenario: Commit manifest
- **WHEN** `commit_prepare` writes its manifest
- **THEN** `manifestPath` starts with `<main worktree>/.sdlc-v2/scratch/sdlc-commit-manifest-`

#### Scenario: Poll state
- **WHEN** `poll_await` returns `pending`
- **THEN** `state_file` is under `<main worktree>/.sdlc-v2/scratch/`

#### Scenario: OpenSpec validation copy
- **WHEN** `openspec_stage` validates a change
- **THEN** the validation copy is under `<main worktree>/.sdlc-v2/scratch/sdlc-openspec-*`
- **AND** the copy is removed when the call returns

#### Scenario: Old poll state path
- **WHEN** a poll that started before the change passes a `state_file` under the system temp folder
- **THEN** `poll_await` reads that path and the poll continues

### Requirement: Old scratch entries are removed
A tool call that creates a scratch entry SHALL remove the entries with the same prefix that are older than 24 hours. A failed folder read or remove SHALL NOT fail the call.

#### Scenario: Stale manifest folder
- **WHEN** `.sdlc-v2/scratch/` holds a `sdlc-harden-*` folder 25 hours old
- **AND** `prepare_orchestrator` runs with mode `harden`
- **THEN** that folder is gone after the call

#### Scenario: Entry that cannot be removed
- **WHEN** the remove of one old entry fails
- **THEN** the call succeeds and the entry stays

### Requirement: Scratch folder create failure
When the scratch folder cannot be created, the tool SHALL return an InfraError that names the folder, with a Suggestion to check that the path is a folder with write permission.

#### Scenario: Scratch path is a file
- **WHEN** `.sdlc-v2/scratch` is a regular file
- **AND** `openspec_stage` runs
- **THEN** the call returns an InfraError whose message contains `create scratch folder`

### Requirement: Cache cleanup covers the scratch folder
The dashboard Clear cache action and the state `gc` sweep SHALL delete old `sdlc-*` entries under `.sdlc-v2/scratch/` of the repository.

#### Scenario: Clear cache
- **WHEN** the user runs Clear cache for a repository with a 2-day-old `sdlc-review-*` folder in `.sdlc-v2/scratch/`
- **THEN** the folder is deleted
