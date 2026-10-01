# tool-setup-init Specification

## Purpose
`setup_init` scaffolds `.sdlc-v2/` (gitignore blocks and full commented TOML templates), and in separate modes checks, reads, or writes the project plan and PR templates. The `setup` skill and its sub-flows call it. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Mode selection
The tool SHALL run exactly one mode per call, chosen by the input fields below. With no mode field set it SHALL run scaffold mode.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `writePlanTemplate` | bool | no | boolean, e.g. `true` | Copy the shipped `plan-template-default.md` to `.sdlc-v2/plan-template.md`. |
| `writePRTemplate` | bool | no | boolean, e.g. `true` | Write `content` to `.sdlc-v2/pr-template.md`. |
| `content` | string | when `writePRTemplate` | Markdown, e.g. `## Summary\n...` | PR template body. Ignored in other modes. |
| `checkPlanTemplate` | bool | no | boolean, e.g. `true` | Report whether `.sdlc-v2/plan-template.md` exists. |
| `checkPRTemplate` | bool | no | boolean, e.g. `true` | Report whether `.sdlc-v2/pr-template.md` exists. |
| `readPlanTemplate` | bool | no | boolean, e.g. `true` | Report existence and full content of `.sdlc-v2/plan-template.md`. |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| More than one mode field is `true` | `DomainError` | `at most one of writePlanTemplate, writePRTemplate, checkPlanTemplate, checkPRTemplate, readPlanTemplate may be true` / set exactly one mode field |

#### Scenario: Two modes rejected
- **WHEN** the caller passes `checkPlanTemplate: true`
- **AND** passes `checkPRTemplate: true`
- **THEN** the tool returns a `DomainError`
- **AND** writes nothing

### Requirement: Output fields
The tool SHALL return the fields below in every mode.

| Field | Meaning |
|---|---|
| `ok` | `false` only when scaffold mode collected at least one per-file error. |
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

### Requirement: Scaffold directories and gitignore blocks
In scaffold mode the tool SHALL create `.sdlc-v2/` and `.sdlc-v2/runs/`, and SHALL write one managed block into `.sdlc-v2/.gitignore` and one into the root `.gitignore`.

`.sdlc-v2/.gitignore` managed block:

```text
# >>> sdlc-v2 managed (do not edit) — selective ignores
*
!.gitignore
!config.toml
!review-dimensions/
!review-dimensions/**
# <<< sdlc-v2 managed
```

Root `.gitignore` managed block:

```text
# >>> sdlc-v2 managed v3 (do not edit) — transient skill artifacts
*-context-*.json
*-manifest-*.json
*-prepare-*.json
# <<< sdlc-v2 managed
```

- An existing block, and the older v1/v2 root blocks, are removed and replaced by the current block.
- Lines outside the block are kept. A bare line equal to a managed pattern is dropped.
- Blank lines outside the block are trimmed at the ends and collapsed to one.
- The block is appended after the user lines.
- Result per file: `created` (file was absent), `changed` (content differed), or nothing (content equal).
- `runs/` is not named in `.sdlc-v2/.gitignore`; the `*` pattern covers it.

#### Scenario: Empty project
- **WHEN** scaffold mode runs in an empty directory
- **THEN** `.sdlc-v2/runs/` exists
- **AND** `.sdlc-v2/.gitignore` contains `!config.toml`
- **AND** the root `.gitignore` contains `*-context-*.json`

#### Scenario: Legacy v1 root block upgraded
- **WHEN** the root `.gitignore` holds `node_modules/`
- **AND** holds a block starting `# >>> sdlc-v2 managed (do not edit) — transient skill artifacts`
- **THEN** after scaffold mode the file contains `managed v3`
- **AND** still contains `node_modules/`
- **AND** no longer contains the v1 block

#### Scenario: No duplicate block on re-run
- **WHEN** scaffold mode runs twice
- **THEN** `.sdlc-v2/.gitignore` contains the begin marker exactly once

### Requirement: Config templates written only when absent
In scaffold mode the tool SHALL write `.sdlc-v2/config.toml` and `.sdlc-v2/local.toml` byte-for-byte from the shipped templates (`plugins/sdlc/templates/config.toml`, `plugins/sdlc/templates/local.toml`) only when each file does not exist. It SHALL NOT modify an existing file.

- `config.toml` top-level keys are exactly `version`, `jira`, `commit`, `pr`, `plan`, `execute`.
- `plan.guardrails` and `execute.guardrails` are named tables.
- `local.toml` carries `ship` and `planStyle`.
- The scaffold passes the config-version check (`needsMigration: false` in `setup_prepare`).
- `next`: `config.toml and local.toml created — instruct the user to edit them by hand, then run the validate tool.`

#### Scenario: First run
- **WHEN** scaffold mode runs in an empty directory
- **THEN** `created` contains `.sdlc-v2/config.toml`
- **AND** `created` contains `.sdlc-v2/local.toml`
- **AND** both files equal the shipped templates

#### Scenario: User edits survive
- **WHEN** `.sdlc-v2/config.toml` exists with user edits
- **AND** scaffold mode runs again
- **THEN** the file keeps the user edits
- **AND** `created` does not contain `.sdlc-v2/config.toml`

### Requirement: Stale JSON config renamed
In scaffold mode the tool SHALL rename `.sdlc-v2/config.json` to `config.json.bak` and `.sdlc-v2/local.json` to `local.json.bak`, on every run, unless the `.bak` file already exists.

- `changed` entry format: `.sdlc-v2/config.json → config.json.bak`.
- When the `.bak` exists, the `.json` file and the `.bak` file are both left unchanged.

#### Scenario: JSON files renamed
- **WHEN** `.sdlc-v2/config.json` exists
- **AND** `.sdlc-v2/local.json` exists
- **THEN** both are renamed to `.bak` with content unchanged
- **AND** `changed` contains `.sdlc-v2/config.json → config.json.bak`
- **AND** `changed` contains `.sdlc-v2/local.json → local.json.bak`

#### Scenario: Backup already present
- **WHEN** `.sdlc-v2/config.json` exists
- **AND** `.sdlc-v2/config.json.bak` exists
- **THEN** both files are unchanged
- **AND** `changed` has no `config.json` entry

#### Scenario: Cleanup after TOML exists
- **WHEN** `.sdlc-v2/config.toml` already exists
- **AND** `.sdlc-v2/config.json` appears
- **THEN** scaffold mode still renames `config.json` to `config.json.bak`

### Requirement: Scaffold error handling
In scaffold mode the tool SHALL return a tool error only when a directory cannot be created. Other file failures SHALL be collected in `errors` with `ok: false`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Cannot create `.sdlc-v2/` | `InfraError` | `create .sdlc-v2 directory: <cause>` / check write permission and disk space |
| Cannot create `.sdlc-v2/runs/` | `InfraError` | `create .sdlc-v2/runs directory: <cause>` / check write permission and disk space |
| A gitignore write, template write, or JSON rename fails | none (result) | `errors` entry, e.g. `config.toml: <cause>`; `ok: false` |

#### Scenario: Template write fails
- **WHEN** writing `.sdlc-v2/local.toml` fails
- **THEN** the tool returns a result with `ok: false`
- **AND** `errors` contains an entry starting `local.toml:`

### Requirement: Check modes
With `checkPlanTemplate` or `checkPRTemplate` the tool SHALL report in `exists` whether `.sdlc-v2/plan-template.md` or `.sdlc-v2/pr-template.md` exists. It SHALL NOT read the file content and SHALL NOT create `.sdlc-v2/`.

- `next`: `.sdlc-v2/<name> exists.` or `.sdlc-v2/<name> does not exist.`
- A stat failure other than "not found" is an `InfraError` `stat <path>: <cause>`.

#### Scenario: Plan template missing
- **WHEN** `checkPlanTemplate` is `true` in an empty directory
- **THEN** `exists` is `false`
- **AND** `.sdlc-v2/` is not created

#### Scenario: PR template present
- **WHEN** `.sdlc-v2/pr-template.md` exists
- **AND** `checkPRTemplate` is `true`
- **THEN** `exists` is `true`
- **AND** `content` is `""`

### Requirement: Read plan template mode
With `readPlanTemplate` the tool SHALL return `exists` and, when the file exists, its full content in `content`. It SHALL NOT create `.sdlc-v2/`.

- Missing file: `exists: false`, `content: ""`, `next`: `.sdlc-v2/plan-template.md does not exist — nothing to show.`
- Present file: `exists: true`, `next`: `Show content to the user before deciding whether to overwrite, or summarize its sections.`
- A read failure other than "not found" is an `InfraError` `read <path>: <cause>`.

#### Scenario: Template present
- **WHEN** `.sdlc-v2/plan-template.md` contains `## Required Sections\n- Summary\n`
- **AND** `readPlanTemplate` is `true`
- **THEN** `exists` is `true`
- **AND** `content` equals that text

### Requirement: Write plan template mode
With `writePlanTemplate` the tool SHALL copy the shipped `plan-template-default.md` from the installed plugin byte-for-byte to `.sdlc-v2/plan-template.md`, creating `.sdlc-v2/` if needed, and SHALL overwrite an existing file.

- `created`: `[".sdlc-v2/plan-template.md"]`.
- `next`: `Plan template written to .sdlc-v2/plan-template.md — now the active template for plan's Step 2 planner.`

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Shipped `plan-template-default.md` not found | `DataError` | `setup_init: shipped plan-template-default.md not found — plugin installation may be corrupt` / update or reinstall the plugin |
| Shipped file unreadable | `InfraError` | `read <path>: <cause>` |
| `.sdlc-v2/` cannot be created, or target cannot be written | `InfraError` | `create .sdlc-v2 directory: <cause>` or `write <path>: <cause>` |

#### Scenario: Copy succeeds
- **WHEN** `writePlanTemplate` is `true`
- **AND** the plugin ships `skills/plan/plan-template-default.md`
- **THEN** `.sdlc-v2/plan-template.md` equals the shipped file byte-for-byte
- **AND** `created` is `[".sdlc-v2/plan-template.md"]`

#### Scenario: Shipped file missing
- **WHEN** no `plan-template-default.md` can be found in any installed plugin location
- **THEN** the tool returns a `DataError`

### Requirement: Write PR template mode
With `writePRTemplate` the tool SHALL write `content` verbatim to `.sdlc-v2/pr-template.md`, creating `.sdlc-v2/` if needed, and SHALL overwrite an existing file without a check.

- `created`: `[".sdlc-v2/pr-template.md"]`.
- `next`: `PR template written to .sdlc-v2/pr-template.md.`

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `content` is empty | `DomainError` | `setup_init: content is required when writePRTemplate is true` / pass the accepted template as `content` |
| `.sdlc-v2/` cannot be created, or target cannot be written | `InfraError` | `create .sdlc-v2 directory: <cause>` or `write <path>: <cause>` |

#### Scenario: Write succeeds
- **WHEN** `writePRTemplate` is `true`
- **AND** `content` is `## PR Template\n\nDescribe the change.\n`
- **THEN** `.sdlc-v2/pr-template.md` equals `content`
- **AND** `created` is `[".sdlc-v2/pr-template.md"]`

#### Scenario: Empty content
- **WHEN** `writePRTemplate` is `true`
- **AND** `content` is omitted
- **THEN** the tool returns a `DomainError`

### Requirement: Project root
The tool SHALL resolve the project root to the main worktree root and fall back to the current working directory when that fails.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root and current directory both unresolvable | `InfraError` | `resolve project root: <cause>` / restart the sdlc MCP server from an existing directory, then retry `setup_init` |

- Annotations: `Title: "Initialize SDLC config files"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: true`, `OpenWorld: false`.

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs from a linked git worktree
- **THEN** all paths are written under the main worktree root
