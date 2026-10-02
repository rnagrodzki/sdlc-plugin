# Spec Delta

## MODIFIED Requirements

### Requirement: Output fields
The tool SHALL return the fields below in every mode.

| Field | Meaning |
|---|---|
| `ok` | `false` only when scaffold mode collected at least one per-file error. |
| `root` | Absolute path of the worktree that holds the git-tracked files (`.sdlc-v2/config.toml`, both `.gitignore` blocks, `plan-template.md`, `pr-template.md`). |
| `created` | Repo-relative paths created. Empty list when none. |
| `changed` | Repo-relative paths updated or renamed. Empty list when none. |
| `next` | Mode-specific guidance text (see each mode). |
| `errors` | Per-file error strings; omitted when none. |
| `exists` | Check/read mode result. Always present; `false` in other modes. |
| `content` | Read mode result. Always present; `""` in other modes. |

#### Scenario: Fields always present
- **WHEN** scaffold mode succeeds
- **THEN** `exists` is `false`
- **AND** `content` is `""`
- **AND** `root` is an absolute path

### Requirement: Project root
The tool SHALL write git-tracked files under the active worktree root and gitignored state under the main worktree root. Each root falls back as shown below.

| File | Root |
|---|---|
| `.sdlc-v2/config.toml` | active worktree (`git rev-parse --show-toplevel`) |
| `.sdlc-v2/.gitignore` managed block, root `.gitignore` managed block | active worktree |
| `.sdlc-v2/plan-template.md`, `.sdlc-v2/pr-template.md` (write, check and read modes) | active worktree |
| `.sdlc-v2/config.json` → `.bak` rename | active worktree |
| `.sdlc-v2/local.toml`, `.sdlc-v2/runs/`, `.sdlc-v2/local.json` → `.bak` rename | main worktree |

- The main worktree root falls back to the current working directory.
- The active worktree root falls back to the main worktree root (or its fallback).

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root and current directory both unresolvable | `InfraError` | `resolve project root: <cause>` / restart the sdlc MCP server from an existing directory, then retry `setup_init` |

- Annotations: `Title: "Initialize SDLC config files"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: true`, `OpenWorld: false`.

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs in scaffold mode from a linked git worktree
- **THEN** `.sdlc-v2/config.toml`, `.sdlc-v2/.gitignore` and `.gitignore` are written under the linked worktree
- **AND** `.sdlc-v2/local.toml` and `.sdlc-v2/runs/` are written under the main worktree
- **AND** `root` is the linked worktree path

#### Scenario: Called outside a git repository
- **WHEN** the current directory is not inside a git worktree
- **THEN** `.sdlc-v2/config.toml` and `.sdlc-v2/local.toml` are both written under the current directory
- **AND** `root` is the current directory
