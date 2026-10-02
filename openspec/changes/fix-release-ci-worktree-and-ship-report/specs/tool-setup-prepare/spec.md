# Spec Delta

## MODIFIED Requirements

### Requirement: CI script drift
The tool SHALL report one `ciScriptDrift` entry per CI script or workflow that `scaffold_ci` installs, comparing the version installed under the active worktree root with the version embedded in the running binary. It SHALL NOT write any file.

| Field | Meaning |
|---|---|
| `script` | Destination path of the script or workflow. |
| `installedVersion` | Version found in the installed file; `0` when not installed. |
| `currentVersion` | Version embedded in the binary. |
| `action` | `current`, `outdated`, or `missing`. |

- `outdated`: installed version is lower than current, or only the legacy file exists.
- `missing`: neither the destination nor the legacy file exists.
- When the active worktree root cannot be resolved, the comparison uses the project root.
- If the comparison fails, `ciScriptDrift` is `[]` and the tool still returns `ok: true`.
- Remediation is `scaffold_ci({force:true})`; see the `tool-scaffold-ci` spec.

#### Scenario: Nothing scaffolded
- **WHEN** no CI script is installed in the project
- **THEN** every `ciScriptDrift` entry has `action: "missing"`
- **AND** `installedVersion: 0`

#### Scenario: Scaffolded in a linked worktree
- **WHEN** `scaffold_ci` ran in a linked worktree and `setup_prepare` runs from the same linked worktree
- **THEN** every `ciScriptDrift` entry has `action: "current"`

### Requirement: Project root and side effects
The tool SHALL resolve the project root to the main worktree root, fall back to the current working directory when that fails, and SHALL be read-only. CI script drift alone reads the active worktree root (see "CI script drift").

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root and current directory both unresolvable | `InfraError` | `resolve project root: <cause>` / restart the sdlc MCP server from an existing directory, then retry `setup_prepare` |

- Annotations: `Title: "Prepare SDLC setup context"`, `ReadOnly: true`, `Idempotent: true`, `OpenWorld: false`.

#### Scenario: Called outside a git repository
- **WHEN** the current directory is not inside a git worktree
- **THEN** the tool uses the current directory as the project root
- **AND** returns `ok: true`
