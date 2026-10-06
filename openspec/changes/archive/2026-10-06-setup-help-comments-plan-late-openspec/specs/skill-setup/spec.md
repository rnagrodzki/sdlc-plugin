## MODIFIED Requirements

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
- A field with `whenStepInActiveSteps` is skipped unless that step is in the `ship.steps` answer.
- `version`: skip `versionFile` and `fileType` when `mode` is `tag`; skip `changelogFile` when `changelog` is `false`; omit an empty `preRelease`.
- When `confirmDetected` is `true` (only `version`), a first AskUserQuestion `Use detected settings, customize each field, or skip this section?` offers `yes`, `customize`, `skip`.
- `yes` writes the detected values without `preRelease`: `{ mode: 'file', versionFile, fileType, tagPrefix }`, or `{ mode: 'tag', tagPrefix }` when no file was detected.
- `skip` writes nothing for the section.

| Field type | Stored value |
|---|---|
| `enum` | Selected option |
| `multi-select` | Array of selected options |
| `boolean` | `yes` → `true`, `no` → `false`; `rebase` keeps `auto`/`skip`/`prompt` |
| `string` | Entered text; omitted when empty and optional |
| `number` | Integer; re-asked when outside `min`/`max` |
| `list` | Comma-split, trimmed array |
| `list` (`narrativeRules`, `instructions`) | One entry per line; empty lines dropped |

#### Scenario: Ship-step gated field
- **WHEN** a field has `whenStepInActiveSteps: "verify-pipeline"`
- **AND** the user's `ship.steps` answer does not include `verify-pipeline`
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
