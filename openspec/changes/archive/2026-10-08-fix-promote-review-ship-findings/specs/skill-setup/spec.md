# Spec Delta

## MODIFIED Requirements

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

### Requirement: Source badge for local sections
The status block SHALL show, on each `set` local section row, the source of its values from `localValues[<section id>].sources`: `(user)`, `(project)`, or `(user+project)`.

#### Scenario: Values from both files
- **WHEN** `localValues.review.sources` is `{scope: "user", maxParallelDimensions: "project"}`
- **THEN** the `review` status row shows `(user+project)`

#### Scenario: Values from the user file only
- **WHEN** every key in `localValues["communication-style"].sources` is `user`
- **THEN** the `communication-style` status row shows `(user)`
