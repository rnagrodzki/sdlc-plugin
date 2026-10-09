# worktree-state-links Specification

## Purpose
Makes run state written by the MCP server under the main worktree visible, live, from every linked git worktree, without ever sharing config.

## Requirements

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

### Requirement: Link creation trigger
The SessionStart hook SHALL create missing links whenever the session's active worktree is a linked worktree, and SHALL create no link in the main worktree. The main worktree is the checkout whose `.git` is a folder.

- Running it again with all links present changes nothing.
- After it creates a link for a folder entry, the hook SHALL create `<main-worktree>/.sdlc-v2/<entry>` when it is missing. `timings.json` gets no folder.

#### Scenario: Main worktree
- **WHEN** a session starts in the main worktree
- **THEN** no symlink is created under `.sdlc-v2/`

#### Scenario: Idempotent
- **WHEN** a session starts twice in the same linked worktree
- **THEN** the second start creates no link and prints no link message

#### Scenario: New folder link gets its main-worktree folder
- **WHEN** a session starts in a linked worktree and `<main-worktree>/.sdlc-v2/run-archive` does not exist
- **THEN** `<linked-worktree>/.sdlc-v2/run-archive` is a symlink to `<main-worktree>/.sdlc-v2/run-archive`
- **AND** `<main-worktree>/.sdlc-v2/run-archive` is a folder
- **AND** `<main-worktree>/.sdlc-v2/timings.json` is not created

### Requirement: Existing entries are never replaced
When a "Link" entry already exists in the linked worktree as a regular file or directory, the system SHALL leave it unchanged and print one line `sdlc: .sdlc-v2/<entry> exists in this worktree — not linked to the main worktree`. This rule covers regular files and directories only. A symlink entry follows the requirement "Linked worktree repairs links".

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
When a link step fails, the hook SHALL skip that entry, print one line, and SHALL NOT fail the session start. The link steps are symlink creation, link removal, relink and main-worktree folder creation.

| Failure | Line |
|---|---|
| symlink creation | `sdlc: could not link .sdlc-v2/<entry>: <cause>` |
| removal in the main worktree | `sdlc: could not remove dangling link .sdlc-v2/<entry>: <cause>` |
| relink in a linked worktree | `sdlc: could not relink .sdlc-v2/<entry>: <cause>` |
| main-worktree folder creation | `sdlc: could not create .sdlc-v2/<entry> in the main worktree: <cause>` |

#### Scenario: Permission denied
- **WHEN** symlink creation fails with a permission error
- **THEN** the session starts normally
- **AND** the output contains `sdlc: could not link .sdlc-v2/<entry>:`

#### Scenario: Folder creation fails
- **WHEN** the main-worktree folder for a new link cannot be created
- **THEN** the session starts normally
- **AND** the output contains `sdlc: could not create .sdlc-v2/<entry> in the main worktree:`

### Requirement: Stray-state check accepts links
The worktree anchoring check SHALL NOT report `WORKTREE_ANCHOR_STRAY_STATE` for a "Link" entry that is a symlink resolving to the same entry under the main worktree's `.sdlc-v2/`, and SHALL still report a regular file or directory, or a symlink that resolves anywhere else.

#### Scenario: Correct link
- **WHEN** `<linked-worktree>/.sdlc-v2/runs` is a symlink to `<main-worktree>/.sdlc-v2/runs`
- **THEN** no `WORKTREE_ANCHOR_STRAY_STATE` finding names `runs`

#### Scenario: Link to a wrong target
- **WHEN** `<linked-worktree>/.sdlc-v2/runs` is a symlink to `/tmp/elsewhere`
- **THEN** a `WORKTREE_ANCHOR_STRAY_STATE` finding names `runs`

### Requirement: Main worktree removes dangling links
In the main worktree, the SessionStart hook SHALL remove each "Link" entry under `.sdlc-v2/` that is a symlink whose target does not exist and ends with `/.sdlc-v2/<entry>`. The hook SHALL keep each other symlink with one advisory line, and SHALL never remove a regular file or directory.

#### Scenario: Dangling reverse link removed
- **WHEN** a session starts in the main worktree and `.sdlc-v2/run-archive` is a symlink to `<deleted-worktree>/.sdlc-v2/run-archive`, which does not exist
- **THEN** `.sdlc-v2/run-archive` does not exist after the session start
- **AND** the output contains `sdlc: removed dangling link .sdlc-v2/run-archive`

#### Scenario: Live link kept
- **WHEN** a session starts in the main worktree and `.sdlc-v2/state` is a symlink to `<target>`, a folder that exists
- **THEN** the symlink is unchanged
- **AND** the output contains `sdlc: .sdlc-v2/state in the main worktree is a link to <target> — kept; the main worktree must hold real state folders`

#### Scenario: Foreign dangling link kept
- **WHEN** a session starts in the main worktree and `.sdlc-v2/reports` is a symlink to `/Volumes/backup/reports`, which does not exist
- **THEN** the symlink is unchanged
- **AND** the output contains `sdlc: .sdlc-v2/reports in the main worktree is a link to /Volumes/backup/reports — kept`

#### Scenario: Real folder kept
- **WHEN** a session starts in the main worktree and `.sdlc-v2/runs` is a real folder
- **THEN** the folder is unchanged
- **AND** the output has no line for `runs`

### Requirement: Linked worktree repairs links
In a linked worktree, the SessionStart hook SHALL replace each "Link" symlink that does not point to the main worktree and whose target does not exist. The hook SHALL keep a symlink to another path that exists, and SHALL create a missing main-worktree folder for a folder entry.

#### Scenario: Wrong dangling link replaced
- **WHEN** `<linked-worktree>/.sdlc-v2/preplan` is a symlink to `/deleted/.sdlc-v2/preplan`, which does not exist
- **THEN** `<linked-worktree>/.sdlc-v2/preplan` is a symlink to `<main-worktree>/.sdlc-v2/preplan`
- **AND** `<main-worktree>/.sdlc-v2/preplan` is a folder
- **AND** the output contains `sdlc: relinked .sdlc-v2/preplan to the main worktree (old target /deleted/.sdlc-v2/preplan did not exist)`

#### Scenario: Correct link gets its folder
- **WHEN** `<linked-worktree>/.sdlc-v2/state` is a symlink to `<main-worktree>/.sdlc-v2/state`, which does not exist
- **THEN** `<main-worktree>/.sdlc-v2/state` is a folder
- **AND** the output has no line for `state`

#### Scenario: Wrong live link kept
- **WHEN** `<linked-worktree>/.sdlc-v2/reports` is a symlink to `/data/reports`, which exists
- **THEN** the symlink is unchanged
- **AND** the output contains `sdlc: .sdlc-v2/reports links to /data/reports, not to the main worktree — kept`

#### Scenario: Main-worktree folder path is a dangling link
- **WHEN** `<main-worktree>/.sdlc-v2/run-archive` is a symlink whose target does not exist
- **THEN** no folder is created
- **AND** the output contains `sdlc: .sdlc-v2/run-archive in the main worktree is a link that points nowhere. Start a session in the main worktree to remove it.`
