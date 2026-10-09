# Design

## Context

- See proposal.md - Why.
- The link phase runs in the SessionStart hook. It treats each existing link as correct and makes each new link dangling until the first write.
- Two writers fail on a dangling link: the dashboard archive writer and `preplan_context`. The dashboard dialog shows only the error message, not the suggestion.

Architecture of the touched parts:

```mermaid
flowchart LR
  subgraph Hooks
    H1[internal/hooks SessionStart link phase]:::changed
  end
  subgraph MCP tools
    T1[dashboard run-archive writer]:::changed
    T2[plan_support preplan_context]:::changed
    T3[validators IsCorrectStateLink]:::changed
  end
  subgraph Internal packages
    F1[internal/fsx dangling-link check]:::new
  end
  subgraph External
    FS[(filesystem .sdlc-v2 links)]
  end
  H1 --> T3
  H1 --> FS
  T1 --> F1
  T2 --> F1
  F1 --> FS
  classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
  classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
```

Main runtime flow for a write through a dangling link:

```mermaid
sequenceDiagram
  actor User
  participant Skill as preplan skill
  participant Tool as plan_support preplan_context
  participant FS as filesystem
  User->>Skill: start a preplan topic
  Skill->>Tool: preplan_context(topic)
  Tool->>FS: create .sdlc-v2/preplan
  FS-->>Tool: mkdir: file exists
  Tool->>FS: walk the path, follow the link chain
  FS-->>Tool: link and missing target
  Tool-->>Skill: InfraError with link, target and recovery
  Skill-->>User: print the error and stop
```

States of one linked entry at session start:

```mermaid
stateDiagram-v2
  [*] --> Missing
  Missing --> CorrectLink: create link and main-worktree folder
  WrongDangling --> CorrectLink: relink
  CorrectDangling --> CorrectLink: create main-worktree folder
  WrongLive --> WrongLive: keep, one line
  RealEntry --> RealEntry: keep, one line
  CorrectLink --> [*]
```

## Goals / Non-Goals

**Goals:**

- One shared check that finds the dangling link on a failed write path.
- Repair at each session start, with one output line for each action or failure.

**Non-Goals:**

- Find which code wrote the reverse links.
- Change other folder writers behind linked entries.
- Show the archive suggestion in the dashboard dialog.

## Decisions

| Decision | Chosen | Rejected alternatives | Reason |
|---|---|---|---|
| Home of the check | `internal/fsx` | `internal/paths`, one check for each writer | `fsx` imports no other internal package. Both writers import it. |
| When the check runs | only after a write fails with `fs.ErrExist` | a check before each write | the normal path has no extra cost |
| Recovery text place | in the error message | a dashboard JS change to show the suggestion | the dialog shows only the message |
| Main-worktree test | `<active root>/.git` is a folder | a path compare only | git gives each linked worktree a `.git` file |
| Link compare | reuse `IsCorrectStateLink` | a new string compare | it treats `/var` and `/private/var` as the same path |
| Folder-link recovery | new session, or `mkdir -p <target>` | remove the link | a removed folder link makes the next write create a real folder in the linked worktree |
| Link chain | report the last link and its missing target (at most 40 links) | report the first link | the first link points to a link that exists, so `mkdir -p` fails |

## Risks / Trade-offs

| Risk | Impact | Mitigation |
|---|---|---|
| The main guard removes links at each session start | a user link could be removed | remove only dangling links whose target ends with `/.sdlc-v2/<entry>`, never a real file or folder |
| Remove then symlink is not atomic | a failed symlink leaves no entry | the next session repairs the missing entry |
| A dangling reverse link in the main worktree | a linked-worktree session cannot repair it | the hook prints a line that says to start a session in the main worktree |
| The `mkdir -p` recovery in the main worktree | creates a folder under the old worktree path | the new-session recovery comes first in the text |
