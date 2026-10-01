# Spec Delta

## Purpose

Makes run state written by the MCP server under the main worktree visible, live, from every linked git worktree, without ever sharing config.

## ADDED Requirements

### Requirement: Linked entry set
In a linked worktree, the system SHALL make each entry in the "Link" column a symlink to the same entry under `<main-worktree>/.sdlc-v2/`, and SHALL never link an entry in the "Never link" column.

| Link (run-generated) | Never link |
|---|---|
| `runs/` | `config.toml`, `.gitignore`, `review-dimensions/` (tracked by git) |
| `reports/` | `local.toml`, `local.json`, `config.json`, `*.bak` (config) |
| `history/` | `jira-templates/` (user templates) |
| `evidence/` | `execution/` (legacy, migrated into `runs/`) |
| `learnings/` | `openspec-staging/` (belongs to the active worktree's branch) |
| `reviews/` | `plan-template.md`, `pr-template.md` (setup-written templates) |
| `state/` | `scratch/`, `backups/` (no code writes them; never created) |
| `timings.json` | |

- Every `.sdlc-v2/` entry the MCP server or hooks write is in exactly one of the two columns.

#### Scenario: Fresh linked worktree
- **WHEN** a session starts in a linked worktree that has no `.sdlc-v2/runs`
- **THEN** `.sdlc-v2/runs` is a symlink whose target is `<main-worktree>/.sdlc-v2/runs`
- **AND** `.sdlc-v2/config.toml` is a regular file, not a symlink

#### Scenario: Live view
- **WHEN** execute writes a state file under `<main-worktree>/.sdlc-v2/runs/`
- **THEN** the same file is readable at `<linked-worktree>/.sdlc-v2/runs/` without any copy step

### Requirement: Link creation trigger
The SessionStart hook SHALL create missing links whenever the session's active worktree is a linked worktree, and SHALL do nothing in the main worktree.

- Running it again with all links present changes nothing.
- A link whose main-worktree target does not exist yet is created anyway (dangling until first write), so later writes appear without a new session.

#### Scenario: Main worktree
- **WHEN** a session starts in the main worktree
- **THEN** no symlink is created under `.sdlc-v2/`

#### Scenario: Idempotent
- **WHEN** a session starts twice in the same linked worktree
- **THEN** the second start creates no link and prints no link message

### Requirement: Existing entries are never replaced
When a "Link" entry already exists in the linked worktree as a regular file or directory, the system SHALL leave it unchanged and print one line `sdlc: .sdlc-v2/<entry> exists in this worktree — not linked to the main worktree`.

#### Scenario: Real directory present
- **WHEN** `<linked-worktree>/.sdlc-v2/reports/` is a real directory with files
- **THEN** it is not deleted or replaced
- **AND** the session-start output contains `sdlc: .sdlc-v2/reports exists in this worktree — not linked to the main worktree`

### Requirement: Git status stays clean
Created links SHALL be gitignored, so `git status --porcelain` in the linked worktree prints no link path.

#### Scenario: Status after linking
- **WHEN** links were created in a linked worktree
- **THEN** `git status --porcelain` lists no path under `.sdlc-v2/`

### Requirement: Fail open
When a symlink cannot be created (for example on Windows without developer mode), the hook SHALL skip that link, print one line `sdlc: could not link .sdlc-v2/<entry>: <cause>`, and SHALL NOT fail the session start.

#### Scenario: Permission denied
- **WHEN** symlink creation fails with a permission error
- **THEN** the session starts normally
- **AND** the output contains `sdlc: could not link .sdlc-v2/<entry>:`

### Requirement: Stray-state check accepts links
The worktree anchoring check SHALL NOT report `WORKTREE_ANCHOR_STRAY_STATE` for a "Link" entry that is a symlink resolving to the same entry under the main worktree's `.sdlc-v2/`, and SHALL still report a regular file or directory, or a symlink that resolves anywhere else.

#### Scenario: Correct link
- **WHEN** `<linked-worktree>/.sdlc-v2/runs` is a symlink to `<main-worktree>/.sdlc-v2/runs`
- **THEN** no `WORKTREE_ANCHOR_STRAY_STATE` finding names `runs`

#### Scenario: Link to a wrong target
- **WHEN** `<linked-worktree>/.sdlc-v2/runs` is a symlink to `/tmp/elsewhere`
- **THEN** a `WORKTREE_ANCHOR_STRAY_STATE` finding names `runs`
