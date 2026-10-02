# tool-setup-prepare Specification

## Purpose
`setup_prepare` returns the static setup section manifest, a config-migration flag, runtime-detected defaults, and CI script drift. The `setup` skill calls it at pre-flight and again after writes. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input fields
The tool SHALL accept the single input field below.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `skipConfigCheck` | bool | optional at call time; missing = `false` | boolean, e.g. `false` | Skip the config-migration check. `needsMigration` is then always `false`. |

#### Scenario: Empty input
- **WHEN** the caller passes `{}`
- **THEN** the tool runs the config-migration check
- **AND** returns `ok: true`

### Requirement: Output fields
The tool SHALL return the fields below and SHALL always return `ok: true` when it returns a result.

| Field | Meaning |
|---|---|
| `ok` | Always `true` on a returned result. |
| `needsMigration` | `true` when the config-migration check fails. See "Config-migration flag". |
| `sections` | 18 section descriptors in canonical order. See "Section manifest". |
| `defaultBranch` | Detected default branch. Omitted when not detected. |
| `remoteOwner` | Owner parsed from the `origin` remote URL. Omitted when not detected. |
| `ciScriptDrift` | One entry per CI script managed by `scaffold_ci`. Always present; may be empty. |

#### Scenario: Fresh temp directory
- **WHEN** the tool runs in an empty directory that is not a git repository
- **THEN** `ok` is `true`
- **AND** `sections` is non-empty
- **AND** the first section has a non-empty `id`
- **AND** the first section has a non-empty `label`

### Requirement: Section manifest
The tool SHALL return exactly 18 section descriptors, in this order: `version`, `ship`, `jira`, `review`, `received-review`, `commit`, `pr`, `github`, `pr-labels`, `review-dimensions`, `pr-template`, `plan-template`, `plan-style`, `plan-tasks`, `plan-guardrails`, `execution-guardrails`, `openspec-block`, `automation`.

Each `sections[]` row:

| Field | Meaning |
|---|---|
| `id` | Canonical section id (list above). |
| `label` | Human label for menus and headers. |
| `purpose` | What the section configures. |
| `configFile` | `.sdlc-v2/config.toml`, `.sdlc-v2/local.toml`, `openspec/config.yaml`, or `<delegated>`. |
| `configPath` | Dotted config path, e.g. `plan.guardrails`, `receivedReview`; `""` for delegated content; `<managed-block>` for `openspec-block`. |
| `consumedBy` | Skills that read this section. |
| `filesModified` | Files the section writes. |
| `optional` | `false` only for `version` and `ship`. |
| `delegatedTo` | Sub-flow name; omitted when the generic field loop applies. |
| `confirmDetected` | `true` only for `version`. |
| `fields` | Per-field descriptors (table below). |

Each `fields[]` entry:

| Field | Meaning |
|---|---|
| `name` | Field name; may be dotted, e.g. `tag.enabled`. |
| `label` | Question prompt. |
| `type` | One of `boolean`, `enum`, `multi-enum`, `multi-select`, `string`, `number`, `list`. |
| `options` | Allowed choices; omitted when empty. |
| `default` | Default value; omitted when unset. |
| `description` | Helper text. |
| `min` / `max` | Numeric bounds; omitted when unset. |
| `whenStepInActiveSteps` | Ask the field only when this step is in `ship.steps`; omitted when unset. |

`delegatedTo` per section:

| `delegatedTo` | Section ids |
|---|---|
| (omitted) | `version`, `ship`, `jira`, `review`, `received-review`, `github`, `plan-style`, `plan-tasks`, `automation` |
| `inline-commit-builder` | `commit` |
| `inline-pr-builder` | `pr` |
| `setup-pr-labels` | `pr-labels` |
| `setup-dimensions` | `review-dimensions` |
| `setup-pr-template` | `pr-template` |
| `setup-plan-template` | `plan-template` |
| `setup-guardrails` | `plan-guardrails` |
| `setup-execution-guardrails` | `execution-guardrails` |
| `setup-openspec` | `openspec-block` |

#### Scenario: Version section fields
- **WHEN** the caller reads the `version` row of `sections`
- **THEN** `fields` is non-empty
- **AND** the first field has `name: "tag.enabled"`
- **AND** that field has `type: "boolean"`

#### Scenario: Review scope field matches review_prepare
- **WHEN** the caller reads the `scope` field of the `review` row
- **THEN** its `default` is `all`, the scope `review_prepare` uses when the key is missing
- **AND** its `description` names no `/review` flag, because `/review` has no scope flag

#### Scenario: openspec-block purpose names this plugin
- **WHEN** the caller reads the `purpose` of the `openspec-block` row
- **THEN** it names `sdlc-v2`
- **AND** it does not name `sdlc-utilities`

#### Scenario: camelCase keys
- **WHEN** the output is serialized
- **THEN** keys use camelCase, e.g. `configFile` and `needsMigration`
- **AND** no PascalCase key such as `ConfigFile` appears

### Requirement: Config-migration flag
The tool SHALL set `needsMigration: true` when `skipConfigCheck` is `false` and a JSON-era `.sdlc-v2/config.json` exists in the project root without `.sdlc-v2/config.toml`. In every other case it SHALL set `needsMigration: false`.

| Project state | `skipConfigCheck` | `needsMigration` |
|---|---|---|
| No `.sdlc-v2/` directory | `false` | `false` |
| `.sdlc-v2/` holds only tool data (no `config.toml`, no `config.json`) | `false` | `false` |
| `.sdlc-v2/config.toml` exists | `false` | `false` |
| `.sdlc-v2/config.json` exists, no `config.toml` | `false` | `true` |
| Any | `true` | `false` |

- The check never produces a tool error.

#### Scenario: JSON-era project
- **WHEN** `.sdlc-v2/config.json` exists without `.sdlc-v2/config.toml`
- **AND** `skipConfigCheck` is `false`
- **THEN** `needsMigration` is `true`

#### Scenario: Check skipped
- **WHEN** `.sdlc-v2/config.json` exists without `.sdlc-v2/config.toml`
- **AND** `skipConfigCheck` is `true`
- **THEN** `needsMigration` is `false`

### Requirement: Runtime defaults
The tool SHALL detect `defaultBranch` and `remoteOwner` on a best-effort basis and SHALL omit each field when detection fails.

- `defaultBranch`: the branch named by `git symbolic-ref refs/remotes/origin/HEAD`; else `main` if `git rev-parse --verify main` succeeds; else `master` if that succeeds.
- `remoteOwner`: the owner parsed from `git remote get-url origin`.
- A detection failure never produces a tool error.

#### Scenario: No git remote
- **WHEN** the project has no `origin` remote
- **THEN** `remoteOwner` is omitted
- **AND** `ok` is `true`

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
