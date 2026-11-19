# skill-setup Specification

## Purpose
The `setup` skill (`/setup`) configures the sdlc plugin for a project: it scaffolds `.sdlc-v2/`, migrates legacy config, and walks the user through a selectable list of 18 config and content sections. Users run it directly, and other skills point to it when config is missing.

## Requirements

### Requirement: Plan mode refusal
The skill SHALL stop before any tool call when plan mode is active.

- Message: "This skill requires write operations. Exit plan mode first, then re-invoke `/setup`."

#### Scenario: Plan mode active
- **WHEN** the system context contains `Plan mode is active`
- **THEN** the skill prints the refusal message
- **AND** calls no MCP tool

### Requirement: Main flow and pre-flight order
The skill SHALL call `setup_prepare({ skipConfigCheck: false })` first, then `setup_init({})` once, then ask the Setup tab question when its conditions hold, then take a config snapshot, before it shows the menu.

Main `/setup` flow without direct-entry flags:

```mermaid
sequenceDiagram
    participant User
    participant Skill as setup skill
    participant P as setup_prepare
    participant I as setup_init
    participant B as dashboard
    participant D as dimensions_render_instructions
    participant M as migrate
    participant W as setup_write_sections
    participant S as scaffold_ci
    participant FS
    Skill->>P: skipConfigCheck false
    P-->>Skill: needsMigration, sections, defaultBranch, remoteOwner, ciScriptDrift, ciScriptDriftError, localValues, userConfigPath
    Skill->>I: scaffold mode, no input
    opt main worktree, no migration
        Skill->>User: AskUserQuestion Setup tab or Dialogue
        opt Setup tab
            Skill->>B: ensure, open true, tab setup
        end
    end
    Skill->>FS: Read .sdlc-v2/config.toml
    Skill->>D: listDimensions true
    Skill->>I: checkPRTemplate true, then checkPlanTemplate true
    Skill->>FS: Read openspec/config.yaml if present
    Skill->>User: status block and numbered menu, plain chat
    User-->>Skill: selection
    opt needsMigration true or --migrate
        Skill->>User: AskUserQuestion migrate?
        Skill->>M: import, then config, then layout
    end
    opt a local section is selected
        Skill->>User: AskUserQuestion save target
    end
    loop each selected section in canonical order
        Skill->>User: section header, then questions or sub-flow
    end
    Skill->>User: diff preview with save target file, AskUserQuestion confirm
    Skill->>W: sectionsJson and target
    Skill->>P: re-snapshot and check written sections
    opt Step 3c: a CI script is outdated or missing
        Skill->>User: CI script table, AskUserQuestion Update or Skip
        opt Update
            Skill->>S: updateOutdated true
            Skill->>P: skipConfigCheck true, fresh drift
        end
    end
    Skill->>User: summary
```

Snapshot contents:

| Item | Source |
|---|---|
| `projectConfig` | Read `.sdlc-v2/config.toml` (absent = `{}`) |
| `localValues`, `userConfigPath` | `setup_prepare` output; the skill does not read `.sdlc-v2/local.toml` or the user file itself |
| Installed dimension count and names | `dimensions_render_instructions({ listDimensions: true })` |
| PR template exists | `setup_init({ checkPRTemplate: true })` |
| Plan template exists | `setup_init({ checkPlanTemplate: true })` |
| Managed-block version | Line `# BEGIN MANAGED BY sdlc-v2 (v<N>)` in `openspec/config.yaml` — the same begin marker `openspec_enrich` writes |

Version detection for the `version` section:

- First existing file in this order sets `versionFile` and `fileType`: `package.json`, `Cargo.toml`, `pyproject.toml`, `pubspec.yaml`, `plugin.json`.
- `tagPrefix` is the common leading non-digit part of recent `git tag --list` tags; default `v` when there are no tags or no common prefix.

#### Scenario: Migration flag read before scaffold
- **WHEN** `.sdlc-v2/config.json` exists without `config.toml`
- **THEN** the skill receives `needsMigration: true` from `setup_prepare`
- **AND** only then calls `setup_init({})`

#### Scenario: Setup tab question before the snapshot
- **WHEN** a plain `/sdlc:setup` run starts in the main worktree with no migration
- **THEN** the skill asks the Setup tab question after `setup_init({})`
- **AND** the skill reads `.sdlc-v2/config.toml` for the snapshot after the answer

#### Scenario: No version file
- **WHEN** none of the five version files exist
- **AND** the repo has no tags
- **THEN** no `versionFile` is detected
- **AND** `tagPrefix` is `v`

#### Scenario: Local values from setup_prepare
- **WHEN** the skill takes the snapshot
- **THEN** it gets the local values from `localValues` of `setup_prepare`
- **AND** it does not read `.sdlc-v2/local.toml`

### Requirement: Flags
The skill SHALL accept the flags below. A direct-entry flag SHALL be translated to `--only <id>` unless `--only` is also passed, and `--only` SHALL skip the menu. `--force` alone SHALL skip the menu and select all 18 ids; with `--only` or a direct-entry flag it SHALL be ignored.

| Flag | Effect |
|---|---|
| `--migrate` | Run the migration step even when `needsMigration` is `false`. |
| `--force` | Menu skipped; all 18 ids selected, including `set` sections. Ignored with `--only` or a direct-entry flag. |
| `--only <ids>` | Comma-separated section ids to configure; menu skipped. Accepts any of the 18 canonical ids. |
| `--dimensions` | Same as `--only review-dimensions`. |
| `--pr-template` | Same as `--only pr-template`. |
| `--guardrails` | Same as `--only plan-guardrails`. |
| `--execution-guardrails` | Same as `--only execution-guardrails`. |
| `--openspec-enrich` | Same as `--only openspec-block`. |
| `--plan-template` | Same as `--only plan-template`. |
| `--remove-openspec` | Passed to the OpenSpec sub-flow as `--remove`. |
| `--add` | Passed to `setup-dimensions`, `setup-guardrails`, `setup-execution-guardrails`. Not passed to `setup-pr-template`, which takes no arguments. |
| `--no-copilot` | Passed to `setup-dimensions`; skips GitHub Copilot instruction files. |

- Canonical ids: `version`, `ship`, `jira`, `review`, `received-review`, `commit`, `pr`, `github`, `pr-labels`, `review-dimensions`, `pr-template`, `plan-template`, `plan-style`, `plan-tasks`, `plan-guardrails`, `execution-guardrails`, `openspec-block`, `automation`.
- `workspace` and `hooks` are not valid ids.
- There is no `--skip` flag.

#### Scenario: Direct entry
- **WHEN** the user runs `/setup --guardrails`
- **THEN** the skill prints no menu
- **AND** configures only `plan-guardrails`

#### Scenario: Explicit id list
- **WHEN** the user runs `/setup --only jira,commit`
- **THEN** the skill prints no menu
- **AND** configures `jira` then `commit`

#### Scenario: Force reconfigures everything
- **WHEN** the user runs `/setup --force`
- **THEN** the skill prints no menu
- **AND** configures all 18 sections in canonical order, including sections already `set`

#### Scenario: Force with an id list
- **WHEN** the user runs `/setup --force --only jira`
- **THEN** the skill configures only `jira`

### Requirement: Menu is plain chat
Without `--only`, `--force`, or a direct-entry flag, the skill SHALL print a status block and a numbered menu as plain chat, then end its turn. It SHALL NOT use AskUserQuestion for the menu.

- Status rows: `[set]` or `[not set]`, the section id, and the row summary (or `—`).
- Menu line: `<N>. [<state>] <label> — <first sentence of purpose>`, `<state>` is `set` or `not-set`.
- 19 lines in canonical order, numbered from 1. After a `Setup tab` answer whose `dashboard` call returned, only the 9 rows of the sections that need the assistant, numbered from 1.
- Labels and purposes come from `setup_prepare`'s `sections`, never hard-coded.
- When `needsMigration` is `true`, one line is printed above the status block: `⚠ Legacy or outdated config detected — see Step 2 (migration) before configuring sections below.`
- The reply prompt lists `all`, `not-set`, `none`, `cancel` and says `Default: all`.

#### Scenario: Menu turn ends
- **WHEN** the skill prints the menu
- **THEN** its turn ends
- **AND** the user's next message is parsed as the reply

#### Scenario: Menu after a tab answer
- **WHEN** the user answered `Setup tab` and the `dashboard` call returned
- **THEN** the status block and the menu list only the rows whose section has a `delegatedTo` value
- **AND** the rows are numbered from 1

### Requirement: Row state
The skill SHALL compute each row's state itself from the snapshot; no tool returns it.

| Section kind | `set` when |
|---|---|
| `review-dimensions` | Installed dimension count > 0 |
| `pr-template`, `plan-template` | The template file exists |
| `openspec-block` | A managed-block line was found |
| `configFile` is `.sdlc-v2/config.toml` | `configPath` resolves in `projectConfig`; an array or a table must have at least one entry |
| `configFile` is `.sdlc-v2/local.toml` (`ship`, `review`, `received-review`, `plan-style`, `github`, `automation`) | `localValues[<section id>].values` is not empty |

- Every other case is `not-set`.

#### Scenario: Empty guardrails table
- **WHEN** `plan.guardrails` resolves to a table with no named guardrail tables
- **THEN** the `plan-guardrails` row is `not-set`

#### Scenario: Guardrail count in the summary
- **WHEN** `config.toml` has `[plan.guardrails.test-coverage-required]` and `[plan.guardrails.no-ci-bypass]`
- **THEN** the `plan-guardrails` row is `set`
- **AND** its summary is `2 configured`

#### Scenario: Managed block written by openspec_enrich
- **WHEN** `openspec_enrich` has written its block into `openspec/config.yaml`
- **AND** the file holds the line `# BEGIN MANAGED BY sdlc-v2 (v2)`
- **THEN** the `openspec-block` row is `set`
- **AND** the snapshot's managed-block version is `2`

#### Scenario: Local section set only in the user file
- **WHEN** `localValues.review.values` is `{scope: "diff"}` and `localValues.review.sources` is `{scope: "user"}`
- **THEN** the `review` row is `set`

### Requirement: Menu reply parsing
The skill SHALL resolve the reply to a set of section ids of the listed menu rows as below, and SHALL allow at most 3 retries on invalid input.

| Reply | Selected ids |
|---|---|
| empty | all listed rows |
| `all` | all listed rows |
| `not-set` | listed rows with state `not-set` |
| `none` or `cancel` | none; prints `No sections selected — no config changes.` and goes to the CI script update step, then the summary |
| Numbers and `M-N` ranges, comma- or space-separated | union of the listed rows at those positions |

- Invalid token: print `Invalid input: "<token>" is not a number, range, or known keyword. Try again.`, then re-print the menu and prompt.
- After 3 failed retries: print `No valid input after 3 attempts — no changes made.` and exit.

#### Scenario: Range reply
- **WHEN** the user replies `1-3,7`
- **THEN** the selected ids are rows 1, 2, 3, and 7

#### Scenario: Out of range
- **WHEN** the user replies `42`
- **THEN** the skill prints `Invalid input: "42" is not a number, range, or known keyword. Try again.`
- **AND** re-prints the menu

#### Scenario: All after a tab answer
- **WHEN** the menu lists only the 9 rows of the sections that need the assistant
- **AND** the user replies `all`
- **THEN** the selected ids are the ids of these 9 rows

#### Scenario: No section selected with an outdated CI script
- **WHEN** the user replies `none`
- **AND** one `ciScriptDrift` entry has `action: "outdated"`
- **THEN** the skill prints `No sections selected — no config changes.`
- **AND** the skill asks `Update the CI scripts listed above?` before the summary

### Requirement: Migration step
The skill SHALL run the migration step only when `needsMigration` is `true` or `--migrate` is passed, and SHALL ask before migrating.

- AskUserQuestion: `Legacy or outdated config files detected. Migrate to the current config format before proceeding?` Options `yes`, `no`.
- On `yes`: call `migrate` three times, in order, each with `dryRun: false`: `action: "import"`, then `action: "config"`, then `action: "layout"`.
- All three `result` values are shown to the user verbatim.
- The `import` action writes a legacy value when the destination key is missing or still holds the template default that `setup_init` wrote; a key the user changed is kept. The skill text says so and does not claim that legacy values are never imported.
- When the `import` result has `skippedKeys`, the skill shows each entry on its own line under `Legacy keys not imported:`.
- The `config` action only checks the schema version; its `result` is `up-to-date`. If it fails, the skill shows the error and stops.
- On `no`: skip migration; legacy files are left untouched.
- After migration, the skill re-calls `setup_prepare` (for `localValues`) and re-reads `.sdlc-v2/config.toml` only.

#### Scenario: Current config
- **WHEN** `needsMigration` is `false`
- **AND** `--migrate` is not passed
- **THEN** the skill calls no `migrate` action

#### Scenario: User accepts migration
- **WHEN** the user answers `yes`
- **THEN** the skill calls `migrate` with `import`, `config`, `layout` in that order
- **AND** the skill does not offer to delete legacy files

#### Scenario: Import skipped keys shown
- **WHEN** the `import` action returns `skippedKeys: [".sdlc-v2/config.toml: jira (already set)", ".sdlc-v2/config.toml: workspace", ".sdlc-v2/local.toml: version (not a section)"]`
- **THEN** the skill shows all three entries, one per line, under `Legacy keys not imported:`

### Requirement: Section dispatch loop
For each selected id, in canonical order, the skill SHALL print a section header and then run the dispatcher named by the section's `delegatedTo`.

- Header lines: `--- Configuring: <label> ----…`, `Purpose:`, `Files modified:`, `Consumed by:`, `Config file: <configFile> (path: <configPath or —>)`, `Current value: <summary or <none>>`.
- When the section has fields, an `Options:` block lists each field's name, type, default, and description, and a line `Examples: <field.examples joined with "; ">` under the description.

| `delegatedTo` | Dispatcher |
|---|---|
| (empty) | Generic field loop |
| `inline-commit-builder` | Commit pattern builder |
| `inline-pr-builder` | PR title pattern builder |
| `setup-dimensions`, `setup-pr-template` | Scan phase, then the sub-flow |
| `setup-pr-labels`, `setup-guardrails`, `setup-execution-guardrails`, `setup-plan-template` | The sub-flow |
| `setup-openspec` | OpenSpec enrichment sub-flow |

#### Scenario: Out-of-order selection
- **WHEN** the user selects rows for `automation` and `version`
- **THEN** the skill configures `version` first

#### Scenario: Examples shown
- **WHEN** the skill prints the `Options:` block for the `ship` section
- **THEN** each field has an `Examples:` line under its description

### Requirement: Generic field loop
For sections with empty `delegatedTo`, the skill SHALL ask exactly one AskUserQuestion per field that survives gating, in manifest order, and SHALL NOT batch or reorder fields.

- Prompt `field.label`, helper text `field.description`, choices `field.options` (free text when empty), default `field.default`.
- When the user asks what the field means, the skill calls `setup_prepare({explain: "<section.id>.<field.name>"})`, shows the details and examples, and asks the same question again.
- When that `explain` call returns an error, the skill shows the error message and asks the same question again.
- The skill holds no option text of its own. Each option string comes from `setup_prepare`.
- `github.expectedAccount` defaults to `remoteOwner` from `setup_prepare`.
- A field with `whenStepInActiveSteps` is skipped unless that step is one of the selected names of the `ship.steps` answer.
- `version`: skip `versionFile` and `fileType` when `mode` is `tag`; skip `changelogFile` when `changelog` is `false`; omit an empty `preRelease`.
- When `confirmDetected` is `true` (only `version`), a first AskUserQuestion `Use detected settings, customize each field, or skip this section?` offers `yes`, `customize`, `skip`.
- `yes` writes the detected values without `preRelease`: `{ mode: 'file', versionFile, fileType, tagPrefix }`, or `{ mode: 'tag', tagPrefix }` when no file was detected.
- `skip` writes nothing for the section.
- The skill does not convert a `flag-set` answer to a table. `setup_write_sections` does the conversion.

| Field type | Stored value |
|---|---|
| `enum` | Selected option |
| `multi-select` | Array of selected options |
| `flag-set` | Asked as a multi-select question; the skill sends the array of selected options, and `setup_write_sections` stores it as a table of every option → `true` or `false` |
| `boolean` | `yes` → `true`, `no` → `false`; `rebase` keeps `auto`/`skip`/`prompt` |
| `string` | Entered text; omitted when empty and optional |
| `number` | Integer; re-asked when outside `min`/`max` |
| `list` | Comma-split, trimmed array |
| `list` (`narrativeRules`, `instructions`) | One entry per line; empty lines dropped |

#### Scenario: Ship-step gated field
- **WHEN** a field has `whenStepInActiveSteps: "verify-pipeline"`
- **AND** the user's `ship.steps` answer does not include `verify-pipeline` among the selected names
- **THEN** the skill does not ask that field
- **AND** does not write a value for it

#### Scenario: Detected version accepted
- **WHEN** `package.json` was detected
- **AND** the user answers `yes` to the detected-settings prompt
- **THEN** the `version` value has `mode: 'file'`
- **AND** has no `preRelease`

#### Scenario: User asks what an option means
- **WHEN** the skill asks the `rebase` field of the `ship` section
- **AND** the user asks what the option means
- **THEN** the skill calls `setup_prepare({explain: "ship.rebase"})`
- **AND** shows the details and examples
- **AND** asks the `rebase` question again

#### Scenario: Flag-set answer sent as an array
- **WHEN** the user selects `execute`, `review` and `harden` for the `steps` field of the `ship` section
- **THEN** the skill sends `"steps": ["execute","review","harden"]` to `setup_write_sections`
- **AND** the `ship` summary line shows the selected steps in pipeline order

### Requirement: Pre-release compatibility check
After the `version` fields are collected with `mode` not `tag` and a non-empty `preRelease`, the skill SHALL look up the chosen `fileType` and act on its level. The check runs at most once per run.

| `fileType` | Level | Action |
|---|---|---|
| `package.json`, `cargo.toml`, `plugin.json` | compatible | Store as-is. |
| `pyproject.toml` | partial | Print message; AskUserQuestion `yes` (store) / `no` (omit `preRelease`). |
| `version-file` | unknown | Print message; AskUserQuestion `yes` (store) / `no` (omit `preRelease`). |
| `pubspec.yaml` | incompatible | Print message; AskUserQuestion `clear` (omit `preRelease`) / `proceed` (store). |

#### Scenario: pubspec pre-release
- **WHEN** `fileType` is `pubspec.yaml`
- **AND** `preRelease` is `rc`
- **THEN** the skill prints that pubspec.yaml does not support semver pre-release labels
- **AND** asks `clear` or `proceed`

### Requirement: Commit and PR pattern builders
The `commit` and `pr` sections SHALL be configured by AskUserQuestion builders, not by the generic field loop.

| Builder | First question | Options |
|---|---|---|
| `commit` | `Do you enforce commit message patterns in this project?` | `conventional`, `ticket-prefix`, `custom`, `skip` |
| `pr` | `Do you enforce PR title patterns?` | `same-as-commit` (only when `commit` was built this run), `conventional`, `ticket-prefix`, `custom`, `skip` |

- `conventional` asks one question each for: scope required, allowed types (default all of `feat, fix, refactor, chore, docs, test, ci`), allowed scopes, required trailers.
- The commit builder also asks which types require a body (`requiresBody`).
- `ticket-prefix` asks the ticket pattern (default `[A-Z]{2,10}-\\d+`) and whether to combine with conventional types.
- `custom` asks for the regex and the mismatch error message (`subjectPattern`/`subjectPatternError` or `titlePattern`/`titlePatternError`).
- `same-as-commit` copies the commit fields, renaming `subjectPattern` → `titlePattern` and `subjectPatternError` → `titlePatternError`.
- `skip` writes no section. Empty optional arrays are omitted.
- The `pr` builder does not collect `defaultBranch` or the GitHub account.

#### Scenario: Conventional commits with scope
- **WHEN** the user picks `conventional`
- **AND** answers `yes` to "Require scope?"
- **THEN** `subjectPattern` starts as `^(feat|fix|refactor|chore|docs|test|ci)(\\(.*\\)): .+$`

#### Scenario: PR same as commit
- **WHEN** `commit` was built this run
- **AND** the user picks `same-as-commit`
- **THEN** the `pr` value has `titlePattern` equal to the commit `subjectPattern`

### Requirement: Scan phase
Before the `setup-dimensions` or `setup-pr-template` sub-flow, the skill SHALL scan the project once per run and pass the result as "Scan Input".

- Signals: dependency manifests, framework configs, directory layout, CI config, database and test layout, `CLAUDE.md`/`AGENTS.md`, `.github/` PR templates, `git remote -v`, `gh repo view`, `gh pr list --limit 5 --json title,body`, `git log --oneline -20`, `git rev-parse --abbrev-ref HEAD`.
- File existence checks use the Glob tool, not shell `ls` globs.
- Dimension list and PR-template existence reuse the pre-flight snapshot.
- A second delegated section in the same run reuses the cached scan.

#### Scenario: Two content sections
- **WHEN** both `review-dimensions` and `pr-template` are selected
- **THEN** the scan runs once

### Requirement: Companion sub-flows run inline
The skill SHALL run each companion sub-flow by reading its sibling Markdown file and following it inline, and SHALL NOT dispatch it through the Agent tool.

| `delegatedTo` | Tools and commands | Writes | User gate | Flags |
|---|---|---|---|---|
| `setup-dimensions` | `dimensions_render_instructions` (list, write, Copilot render; `overwrite: true` only for a confirmed overwrite), `validate({ action: "dimensions" })` | `.sdlc-v2/review-dimensions/<name>.md`; `.github/instructions/<name>.instructions.md` | `Install which dimensions?` (`all`/`select`/`cancel`); `Fix these validation errors automatically?` (`yes`/`no`) when validation has findings; `Generate these Copilot instruction files?` | `--add`, `--no-copilot` |
| `setup-pr-template` | `setup_init({ writePRTemplate: true, content })`, `validate({ action: "pr_template" })` | `.sdlc-v2/pr-template.md` | `Accept this PR template?` loop until `accept` | none (the sub-flow takes no arguments) |
| `setup-pr-labels` | `gh label list --json name,description --limit 100`, `setup_write_sections` | `pr.labels` | `pr.labels is already configured. What do you want to do?` (`keep`/`replace`/`append`) when set; `How should /pr choose labels?` (`off`/`rules`/`llm`/`cancel`) | none |
| `setup-guardrails` | `setup_write_sections`, `validate({ action: "guardrails", section: "plan" })` | `plan.guardrails.<id>` | `Install which guardrails?`; `Add custom project-specific guardrails?` | `--add` |
| `setup-execution-guardrails` | `setup_write_sections`, `validate({ action: "guardrails", section: "execute" })` | `execute.guardrails.<id>` | `Install which execution guardrails?` | `--add` |
| `setup-plan-template` | `setup_init({ readPlanTemplate: true })`, `setup_init({ writePlanTemplate: true })` | `.sdlc-v2/plan-template.md` | `replace`/`cancel` when the file exists | none |

- `setup-dimensions` proposes a dimension only when its evidence row in `dimension-catalog.md` matches the scan.
- `setup-pr-labels` writes nothing when `gh label list` fails or the user picks `cancel`.
- `setup-plan-template` on `cancel` prints `No changes made — existing .sdlc-v2/plan-template.md kept.`

#### Scenario: gh not authenticated
- **WHEN** `pr-labels` is selected
- **AND** `gh label list` exits non-zero
- **THEN** the sub-flow prints a `gh auth login` hint
- **AND** `.sdlc-v2/config.toml` is not written

#### Scenario: Existing plan template kept
- **WHEN** `.sdlc-v2/plan-template.md` exists
- **AND** the user picks `cancel`
- **THEN** the skill does not call `setup_init({ writePlanTemplate: true })`

### Requirement: OpenSpec enrichment sub-flow
For the `openspec-block` section the skill SHALL call `openspec_enrich({ remove })`, with `remove: true` only when `--remove-openspec` was passed, and SHALL report the `action` as below.

| `action` | Reported message |
|---|---|
| `append` | `Managed block added to openspec/config.yaml.` |
| `update` | `Managed block updated to v{version} in openspec/config.yaml.` |
| `unchanged` | `openspec/config.yaml already at current version — no changes needed.` |
| `removed` | `Managed block removed from openspec/config.yaml.` |
| `missing` | `openspec/config.yaml not found. Initialize OpenSpec first (openspec init).` |
| `skipped-existing-context` | Explains that a top-level `context:` key exists and the block was not appended. |

- A `warning` in the result is shown to the user.

#### Scenario: Remove flag
- **WHEN** the user runs `/setup --openspec-enrich --remove-openspec`
- **THEN** the skill calls `openspec_enrich({ remove: true })`

### Requirement: Diff preview before writing
Before any write in the "Writing config files" step, the skill SHALL show a `path | before | after` table of changed config paths and SHALL ask for confirmation with AskUserQuestion.

- Only paths whose value changed are listed.
- No changed path: print `No changes — nothing to write.` and skip the write.
- Rejected: print `Write cancelled — no config changes.` and skip the write. The CI script update step still runs after it, so the run can still write CI scripts.

#### Scenario: Nothing changed
- **WHEN** every assembled value equals the snapshot value
- **THEN** the skill prints `No changes — nothing to write.`
- **AND** does not call `setup_write_sections`

### Requirement: Config writes go through setup_write_sections
The skill SHALL write config only through `setup_write_sections`, using each section's `configPath` as the key, and SHALL NOT use the Write or Edit tools on config files.

| Section | Write key |
|---|---|
| `version`, `ship`, `jira`, `review`, `commit`, `github`, `automation` | same as id |
| `received-review` | `receivedReview` |
| `plan-style` | `planStyle` |
| `plan-tasks` | `plan.tasks` |
| `pr` | `pr`, written wholesale |

- Keys for skipped or unselected sections are omitted.
- Before writing `pr`, the skill reads `.sdlc-v2/config.toml` and copies any existing `pr.labels` into the `pr` value unchanged.
- `pr.labels`, `plan.guardrails.<id>`, and `execute.guardrails.<id>` are written only by their sub-flows, as dotted keys. No sub-flow writes a whole guardrails table.
- The skill shows `written` and any `errors` from the response.

#### Scenario: PR labels preserved
- **WHEN** `pr.labels` already exists
- **AND** the user rebuilds `pr`
- **THEN** the `pr` object sent to `setup_write_sections` contains the existing `labels`

#### Scenario: Plan tasks leaf
- **WHEN** `plan-tasks` is configured
- **THEN** the skill writes key `plan.tasks`, not `plan`

### Requirement: Post-write check
After writing, the skill SHALL re-call `setup_prepare`, re-read `.sdlc-v2/config.toml`, and confirm every written section now has state `set`. It SHALL NOT show the summary while a written section reads `not-set`.

- Local sections are checked against `localValues` of the new `setup_prepare` result; the skill does not read `.sdlc-v2/local.toml` or the user file.

#### Scenario: Write did not land
- **WHEN** a written section still reads `not-set`
- **THEN** the skill warns the user
- **AND** offers to retry that section's write

#### Scenario: User-file write checked
- **WHEN** the skill wrote `review` with `target: "user"`
- **THEN** the re-called `setup_prepare` shows `localValues.review.sources` with `user` for each written key

### Requirement: Summary and learning capture
The skill SHALL end with a `Setup complete` summary that lists only the config files, content, and migrations that were created, updated, or migrated, and SHALL log a learning with `learnings_log({ action: "append", entry })`.

- Learning entry heading: `## YYYY-MM-DD — setup: <brief summary>`.
- The summary SHALL also show a `CI scripts needing an update:` block built from `ciScriptDrift` of the latest drift result (the re-check of the CI script update step when it made one): one line per entry whose `action` is `outdated` or `missing`, as `<script> — <action> (installed v<installedVersion>, current v<currentVersion>)`, followed by the fix `Fix: run /setup again and choose Update, or call scaffold_ci({ updateOutdated: true }).` Any other `action` value counts as `current`, the same rule as the CI script update step. The block is omitted when no entry is shown.
- When `ciScriptDriftError` of that result is not empty, the summary SHALL print `CI script check failed. Tool error text (setup_prepare):` and then the value in a `text` fence, in place of the block. The summary is the only place that prints the drift error.
- When the re-check of the CI script update step returned a tool error, the summary SHALL omit the block and print `Run /setup again to check the CI scripts.`

#### Scenario: Only jira configured
- **WHEN** only `jira` was written
- **THEN** the summary lists `.sdlc-v2/config.toml`
- **AND** omits the content and migration groups

#### Scenario: Outdated CI script shown
- **WHEN** the latest `setup_prepare` returns `ciScriptDrift` with one `outdated` entry and one `current` entry
- **THEN** the summary has a `CI scripts needing an update:` block with one line, for the `outdated` script
- **AND** the block names `scaffold_ci({ updateOutdated: true })` as the fix

#### Scenario: All CI scripts current
- **WHEN** every `ciScriptDrift` entry has `action: "current"`
- **THEN** the summary has no `CI scripts needing an update:` block

#### Scenario: Drift check failed
- **WHEN** the latest `setup_prepare` returns a non-empty `ciScriptDriftError`
- **THEN** the summary prints `CI script check failed. Tool error text (setup_prepare):` and the value in a `text` fence
- **AND** the summary has no `CI scripts needing an update:` block

#### Scenario: Re-check failed
- **WHEN** the user picks Update and the `setup_prepare` re-check returns a tool error
- **THEN** the CI script update step prints the error in a `text` fence
- **AND** the summary has no `CI scripts needing an update:` block
- **AND** the summary prints `Run /setup again to check the CI scripts.`

### Requirement: Save target for personal settings
When at least one selected section has a `defaultTarget`, the skill SHALL ask `Where should setup save your personal settings?` once per run with AskUserQuestion, before the first local section.

| Option | `target` per local section |
|---|---|
| `Defaults per section (Recommended)` | the section's `defaultTarget` |
| `All to this project` | `project` |
| `All to user profile` | `user` |

- No local section selected: no question.
- The answer is not stored; a rerun asks again.

#### Scenario: One question for two local sections
- **WHEN** the user selects `review` and `communication-style`
- **THEN** the skill asks `Where should setup save your personal settings?` once

#### Scenario: No local section
- **WHEN** the user selects only `jira` and `commit`
- **THEN** the skill asks no save-target question

### Requirement: Local section writes pass the save target
The skill SHALL pass the chosen `target` on each `setup_write_sections` call that writes a local section, and SHALL pass no `target` for a project section.

- The diff preview prints the save target file above the rows of each local section.

#### Scenario: Defaults per section
- **WHEN** the user picks `Defaults per section (Recommended)` and configures `review` and `ship`
- **THEN** the skill writes `review` with `target: "user"`
- **AND** writes `ship` with `target: "project"`

#### Scenario: Project section has no target
- **WHEN** the user picks `All to user profile` and configures `jira`
- **THEN** the `setup_write_sections` call for `jira` has no `target`

#### Scenario: Diff preview names the file
- **WHEN** `review` is saved to the user file at `userConfigPath` `/home/a/.sdlc/local.toml`
- **THEN** the diff preview prints `/home/a/.sdlc/local.toml` above the `review` rows

### Requirement: Source badge for local sections
The status block SHALL show, on each `set` local section row, the source of its values from `localValues[<section id>].sources`: `(user)`, `(project)`, or `(user+project)`.

#### Scenario: Values from both files
- **WHEN** `localValues.review.sources` is `{scope: "user", maxParallelDimensions: "project"}`
- **THEN** the `review` status row shows `(user+project)`

#### Scenario: Values from the user file only
- **WHEN** every key in `localValues["communication-style"].sources` is `user`
- **THEN** the `communication-style` status row shows `(user)`

### Requirement: Guardrail sub-flows write one id at a time
The `setup-guardrails` and `setup-execution-guardrails` sub-flows SHALL write one `<section>.guardrails.<id>` key for each selected guardrail, in replace mode and in `--add` mode, and SHALL NOT send a whole guardrails table.

- Setup never removes a guardrail. The only removal path is `null` on one `<section>.guardrails.<id>` key.

#### Scenario: Replace mode sends one key for each id
- **WHEN** the user runs `/setup --guardrails` without `--add` and selects the ids `dry` and `kiss`
- **THEN** the skill calls `setup_write_sections` with the keys `plan.guardrails.dry` and `plan.guardrails.kiss`
- **AND** the call holds no `plan.guardrails` key

#### Scenario: Unselected id stays
- **WHEN** `config.toml` holds the guardrail `no-ci-bypass` that `/harden` added
- **AND** the user selects other ids in replace mode
- **THEN** `[plan.guardrails.no-ci-bypass]` is unchanged

#### Scenario: Warning in both modes
- **WHEN** the sub-flow reaches the selection prompt in replace mode or in `--add` mode
- **THEN** the skill warns that a selected id replaces the same id of `config.toml`

#### Scenario: Call succeeds
- **WHEN** `setup_write_sections` returns `ok: true`
- **THEN** the skill runs the validation step

#### Scenario: Call returns per-key errors
- **WHEN** `setup_write_sections` returns `ok: false`
- **THEN** the skill shows each entry of `errors` and the `next` text
- **AND** the skill skips validation and stops
- **AND** the skill does not edit `.sdlc-v2/config.toml` by hand

#### Scenario: Refusal is not worked around
- **WHEN** the tool returns an error containing `would remove guardrails`
- **THEN** the skill shows the error, skips validation and stops
- **AND** the skill does not edit `.sdlc-v2/config.toml` by hand

#### Scenario: Another tool error
- **WHEN** the tool returns an error for invalid TOML, an unreadable file, or a file over 1 MiB
- **THEN** the skill shows the error and its Suggestion, skips validation and stops

#### Scenario: Interrupted call
- **WHEN** a call stopped part way and some ids are written
- **AND** the user runs the sub-flow again
- **THEN** each id write replaces only that id, and the second run gives the same result

### Requirement: Dimension mirrors overwrite only after confirmation
The `setup-dimensions` sub-flow SHALL pass `overwrite: true` to `dimensions_render_instructions` only for a mirror whose overwrite the user confirmed.

#### Scenario: No mirror file
- **WHEN** no mirror file exists for the dimension
- **THEN** the skill calls the tool without `overwrite`

#### Scenario: Confirmed overwrite
- **WHEN** `.github/instructions/security.instructions.md` exists
- **AND** the user confirms the overwrite
- **THEN** the skill calls `dimensions_render_instructions` with `overwrite: true`

#### Scenario: Declined overwrite
- **WHEN** the mirror exists and the user declines the overwrite
- **THEN** the skill skips this dimension and does not call the tool

#### Scenario: Regeneration in add mode
- **WHEN** `/setup --dimensions --add` regenerates an existing mirror after a change of `_common.md`
- **AND** the user did not confirm an overwrite of that mirror
- **THEN** the skill calls the tool without `overwrite`
- **AND** the tool replaces a mirror with a valid marker line and refuses any other

#### Scenario: Hand-written mirror refused
- **WHEN** the tool returns an error containing `has hand-written content`
- **THEN** the skill shows the error and skips that mirror
- **AND** the skill tells the user to move the hand-written rows into the dimension file, then run `/setup --dimensions` and confirm the overwrite

#### Scenario: Any other error
- **WHEN** the tool returns another error, for example an unparseable dimension
- **THEN** the skill shows the error and skips this dimension

### Requirement: CI script update question
The skill SHALL run a CI script update step (Step 3c) before the summary on every route of the setup skill file that reaches the summary (a companion sub-flow that stops the run ends before it), and SHALL ask one AskUserQuestion only when a `ciScriptDrift` entry has `action` `outdated` or `missing`.

- An absent `ciScriptDrift` counts as empty. Any other `action` value counts as `current`.
- Setup `--force` and `--only` select sections, not files. They do not skip the question.

#### Scenario: Outdated script asks
- **WHEN** the latest `setup_prepare` returns one `ciScriptDrift` entry with `action: "outdated"`
- **THEN** the skill prints a table with the script, its state, the installed version and the current version
- **AND** the skill asks `Update the CI scripts listed above?` with the options `Update (Recommended)` and `Skip`

#### Scenario: All scripts current
- **WHEN** every `ciScriptDrift` entry has `action: "current"`
- **THEN** the skill asks no CI script question
- **AND** the skill does not call `scaffold_ci`

#### Scenario: Unknown action value
- **WHEN** a `ciScriptDrift` entry has `action: "stale"`
- **THEN** the entry counts as `current`
- **AND** the skill asks no CI script question for it

#### Scenario: Force flag still asks
- **WHEN** `/setup --force` runs
- **AND** one `ciScriptDrift` entry has `action: "missing"`
- **THEN** the skill asks `Update the CI scripts listed above?`

### Requirement: CI script update answers
The skill SHALL call `scaffold_ci({ updateOutdated: true })` only after the user picks Update, SHALL then call `setup_prepare({ skipConfigCheck: true })` again, and SHALL build the summary drift block from that second call.

Order of calls after the user picks Update:

```mermaid
sequenceDiagram
    participant User
    participant Skill as setup skill
    participant S as scaffold_ci
    participant P as setup_prepare
    participant FS as filesystem
    Skill->>User: AskUserQuestion Update or Skip
    User-->>Skill: Update
    Skill->>S: updateOutdated true
    S->>FS: write outdated and missing CI scripts
    S-->>Skill: files, warnings, next
    Skill->>P: skipConfigCheck true
    P->>FS: read installed CI scripts
    P-->>Skill: ciScriptDrift, ciScriptDriftError
    Skill->>User: summary
```

#### Scenario: Update succeeds
- **WHEN** the user picks `Update (Recommended)`
- **AND** `scaffold_ci` returns a result
- **THEN** the skill prints each `files` entry with `action` `created`, `overwritten` or `migrated`, then each warning and `next`
- **AND** the skill calls `setup_prepare({ skipConfigCheck: true })` before the summary

#### Scenario: Update writes nothing
- **WHEN** the user picks `Update (Recommended)`
- **AND** `scaffold_ci` returns no `files` entry with `action` `created`, `overwritten` or `migrated`
- **THEN** the skill prints `No CI file needed a change.`

#### Scenario: Update fails
- **WHEN** the user picks `Update (Recommended)`
- **AND** `scaffold_ci` returns an error
- **THEN** the skill prints `Tool error text (scaffold_ci):` and the error and its Suggestion in a `text` fence
- **AND** the skill prints `Some CI files can already be written. Run git status to see them, then run /setup again.`
- **AND** the skill calls `setup_prepare({ skipConfigCheck: true })` before the summary

#### Scenario: Skip
- **WHEN** the user picks `Skip`
- **THEN** the skill does not call `scaffold_ci`

#### Scenario: Other answer
- **WHEN** the user answers with free text or dismisses the question
- **THEN** the skill treats the answer as `Skip`
- **AND** the skill does not call `scaffold_ci`

#### Scenario: No question tool
- **WHEN** AskUserQuestion is not available
- **THEN** the skill treats the answer as `Skip`
- **AND** the skill does not call `scaffold_ci`

### Requirement: CI script check failure
The skill SHALL ask no CI script question and SHALL NOT call `scaffold_ci` when the latest `setup_prepare` returns a non-empty `ciScriptDriftError`. The CI script update step SHALL NOT print the error; the summary prints it once.

#### Scenario: Check failed
- **WHEN** the latest `setup_prepare` returns `ciScriptDriftError` with a size error
- **THEN** the skill asks no CI script question
- **AND** the skill does not call `scaffold_ci`
- **AND** the summary prints `CI script check failed. Tool error text (setup_prepare):` followed by the error and its Suggestion in a `text` fence, once

### Requirement: Setup tab pointer at the end
The summary SHALL end with a pointer to the dashboard Setup tab on every run that reaches the summary. After a tab answer the pointer uses the address that the question step kept; on every other run it comes from `dashboard` with `action:"status"`.

#### Scenario: Form sections deferred to the Setup tab
- **WHEN** the user answered `Setup tab` and the `dashboard` call returned
- **THEN** the summary has a `Deferred` line that says this run did not save the form sections
- **AND** the pointer says to save the form sections at `<url>#setup`, with no `status` call

#### Scenario: Server running
- **WHEN** `status` reports `running:true`
- **THEN** the pointer names `<url>#setup`

#### Scenario: Server not running
- **WHEN** `status` reports `running:false`
- **THEN** the pointer names `/sdlc:dashboard`

#### Scenario: Status error
- **WHEN** the `status` call returns an error
- **THEN** the skill prints the error in a `text` fence and the `running:false` pointer

#### Scenario: Linked worktree
- **WHEN** the run is in a linked worktree
- **THEN** the pointer also says that the Setup tab changes the config of the main worktree

### Requirement: Setup tab in the T3 preview tab
When the `dashboard` output has `previewUrl`, the setup skill SHALL open the Setup tab with the T3 `preview_open` tool.

#### Scenario: Setup tab in T3 Code
- **WHEN** the user picks the dashboard Setup tab in a T3 thread
- **THEN** the skill calls `preview_open` with `previewUrl`

#### Scenario: Setup tab fails
- **WHEN** `preview_open` fails
- **THEN** the skill prints the address and one warning line and continues

### Requirement: Setup tab question before the section menu
The skill SHALL ask once, after `setup_init({})` and before the config snapshot and the menu, whether to change the form sections in the dialogue or in the dashboard Setup tab, only when all 3 conditions hold: no `--only`, `--force` or direct-entry flag, the main worktree is active, and no migration runs.

```mermaid
sequenceDiagram
  participant U as user
  participant K as setup skill
  participant T as dashboard tool
  K->>U: dialogue or Setup tab
  U-->>K: Setup tab
  K->>T: ensure, open true, tab setup
  T-->>K: URL
  K-->>U: Setup tab address
  K->>U: menu of the sections that need the assistant
```

#### Scenario: Tab answer
- **WHEN** the user answers `Setup tab`
- **THEN** the skill calls `dashboard` with `action:"ensure"`, `open:true` and `tab:"setup"`
- **AND** the menu lists only the sections that need the assistant

#### Scenario: Existing config
- **WHEN** both config files already exist before the run
- **THEN** the skill asks the question

#### Scenario: Dialogue answer
- **WHEN** the user answers `Dialogue`, answers with free text, or dismisses the question
- **THEN** the menu lists all 19 sections

#### Scenario: Dashboard error
- **WHEN** the `dashboard` call returns an error
- **THEN** the skill prints the error in a `text` fence
- **AND** the menu lists all 19 sections

#### Scenario: Legacy config
- **WHEN** a JSON-era config needs migration, or `--migrate` is passed
- **THEN** the skill does not ask the question

#### Scenario: Direct-entry flag
- **WHEN** `--only`, `--force` or a direct-entry flag is passed
- **THEN** the skill does not ask the question

#### Scenario: Linked worktree
- **WHEN** the run is in a linked worktree
- **THEN** the skill does not ask the question
