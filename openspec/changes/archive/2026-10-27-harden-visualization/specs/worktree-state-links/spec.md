# Spec Delta

## MODIFIED Requirements

### Requirement: Linked entry set
In a linked worktree, the system SHALL make each entry in the "Link" column a symlink to the same entry under `<main-worktree>/.sdlc-v2/`, and SHALL never link an entry in the "Never link" column.

| Link (run-generated) | Never link |
|---|---|
| `runs/` | `config.toml`, `.gitignore`, `review-dimensions/` (tracked by git) |
| `reports/` | `local.toml`, `local.json`, `config.json`, `*.bak` (config) |
| `history/` | `jira-templates/` (user templates) |
| `evidence/` | `execution/` (legacy, migrated into `runs/`) |
| `learnings/` | `openspec-staging/` (written and read under the main worktree directly, so no link is needed) |
| `reviews/` | `plan-template.md`, `pr-template.md` (setup-written templates) |
| `state/` | `scratch/`, `backups/` (no code writes them; never created) |
| `preplan/` | |
| `run-archive/` | |
| `timings.json` | |

- Every `.sdlc-v2/` entry the MCP server or hooks write is in exactly one of the two columns.

#### Scenario: Fresh linked worktree
- **WHEN** a session starts in a linked worktree that has no `.sdlc-v2/runs`
- **THEN** `.sdlc-v2/runs` is a symlink whose target is `<main-worktree>/.sdlc-v2/runs`
- **AND** `.sdlc-v2/config.toml` is a regular file, not a symlink

#### Scenario: Live view
- **WHEN** execute writes a state file under `<main-worktree>/.sdlc-v2/runs/`
- **THEN** the same file is readable at `<linked-worktree>/.sdlc-v2/runs/` without any copy step

#### Scenario: Preplan and archive folders are linked
- **WHEN** a session starts in a linked worktree that has no `.sdlc-v2/preplan` and no `.sdlc-v2/run-archive`
- **THEN** both are symlinks to the same entries under `<main-worktree>/.sdlc-v2/`

#### Scenario: Staging folder is not linked
- **WHEN** a session starts in a linked worktree that has no `.sdlc-v2/openspec-staging`
- **THEN** no `.sdlc-v2/openspec-staging` symlink is created in the linked worktree
