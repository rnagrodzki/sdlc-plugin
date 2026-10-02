# Spec Delta

## MODIFIED Requirements

### Requirement: Output fields
The tool SHALL return the fields below.

| Field | Meaning |
|---|---|
| `ok` | `true` when every section was written. |
| `written` | Section keys written, in sorted order. |
| `root` | Absolute path of the worktree that holds the written `.sdlc-v2/config.toml` and any scaffolded CI files. |
| `errors` | One `section <key>: <cause>` entry per failed section; omitted when none. |
| `scaffold` | CI file reports from the `version` auto-scaffold; omitted when it did not run. |
| `warnings` | Full-rewrite fallback warnings (see "File text kept outside the written section") and scaffold warnings; omitted when none. |

#### Scenario: Single section written
- **WHEN** `sectionsJson` is `{"commit":{"style":"conventional"}}`
- **THEN** `ok` is `true`
- **AND** `written` is `["commit"]`

### Requirement: CI scaffold after version write
When `version` is in `written`, the tool SHALL run the `scaffold_ci` logic once with force off, under the same root as `.sdlc-v2/config.toml`, and SHALL report its file reports in `scaffold`. Scaffold failure SHALL NOT fail the call.

- `scaffold[]` entries: `path`, `action`, `installedVersion`, `currentVersion`, `group`. See the `tool-scaffold-ci` spec for the file list.
- Scaffold warnings are appended to `warnings`.
- A scaffold error becomes a `warnings` entry `scaffold_ci: <cause>`; `ok` is unaffected.
- No scaffold runs when `version` is not written.

#### Scenario: First version write
- **WHEN** `sectionsJson` is `{"version":{"tag.enabled":true,"tag.prefix":"v"}}` in an empty directory
- **THEN** `scaffold` has 8 entries, each with `action: "created"`
- **AND** `.github/workflows/release-on-main.yml` exists

#### Scenario: Repeat version write
- **WHEN** the same call runs a second time
- **THEN** `ok` is `true`
- **AND** every `scaffold` entry has `action: "skipped"`

#### Scenario: Non-version write
- **WHEN** `sectionsJson` is `{"commit":{"style":"conventional"}}`
- **THEN** `scaffold` is omitted
- **AND** `.github/workflows/release-on-main.yml` is not created

### Requirement: Project root
The tool SHALL write `.sdlc-v2/config.toml` and the auto-scaffolded CI files under the active worktree root, SHALL write `.sdlc-v2/local.toml` under the main worktree root, and SHALL use the current working directory for a root that cannot be resolved.

| File | Root | Why |
|---|---|---|
| `.sdlc-v2/config.toml` | active worktree (`git rev-parse --show-toplevel`) | git-tracked; the change belongs to the checked-out branch |
| `.github/scripts/*`, `.github/workflows/*` | active worktree | git-tracked |
| `.sdlc-v2/local.toml` | main worktree | gitignored per-user state shared by all worktrees |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| A root and the current directory both unresolvable | `InfraError` | `resolve project root: <cause>` / restart the sdlc MCP server from an existing directory, then retry `setup_write_sections` |

- Annotations: `Title: "Write SDLC config sections"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: false`, `OpenWorld: false`.

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs from a linked git worktree with `sectionsJson` `{"version":{"tag.enabled":true},"review":{"scope":"diff"}}`
- **THEN** `.sdlc-v2/config.toml` and `.github/workflows/release-on-main.yml` are written under the linked worktree
- **AND** `.sdlc-v2/local.toml` is written under the main worktree
- **AND** `root` is the linked worktree path
