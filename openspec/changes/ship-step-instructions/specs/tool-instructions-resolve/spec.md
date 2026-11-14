# Spec Delta

## Purpose

MCP tool `instructions_resolve` returns the merged follow-up instructions of one standalone skill, so the skill can run them after success.

## ADDED Requirements

### Requirement: Resolve the list of one skill
The tool SHALL take `key`, one of execute, commit, review, received-review, pr, harden, verify-pipeline, and SHALL return `key`, `count`, `instructions`, `summary` and `next`. It SHALL write nothing.

#### Scenario: List present
- **WHEN** the key `pr` has 2 instructions
- **THEN** `count` is 2 and `instructions` holds `pr #1 (team)` and `pr #2 (project local)`

#### Scenario: No list
- **WHEN** the key `review` has no instruction
- **THEN** `count` is 0, `instructions` is `(none)`, and `next` says `No instructions for review. Continue the skill.`

### Requirement: Config root
The tool SHALL read the config files of the main worktree, else of the current folder.

#### Scenario: Linked worktree
- **WHEN** the tool runs in a linked worktree that has its own `.sdlc-v2/local.toml`
- **THEN** the tool reads the files of the main worktree

### Requirement: Errors
The tool SHALL return the error class of each row, with a Suggestion.

| Case | Class |
|---|---|
| key empty or not standalone | DomainError `<key> runs only in a ship run` |
| bad config content | DataError that names the file, the field, the problem and the fix |
| config file cannot be read | InfraError |

#### Scenario: Ship-only key
- **WHEN** the key is `commit-fixes`
- **THEN** the tool returns DomainError `commit-fixes runs only in a ship run`
