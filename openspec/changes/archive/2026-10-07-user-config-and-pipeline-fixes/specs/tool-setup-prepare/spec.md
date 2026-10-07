# Spec Delta

## MODIFIED Requirements

### Requirement: Output fields
The tool SHALL return the fields below and SHALL always return `ok: true` when it returns a result.

| Field | Meaning |
|---|---|
| `ok` | Always `true` on a returned result. |
| `needsMigration` | `true` when the config-migration check fails. See "Config-migration flag". |
| `sections` | 19 section descriptors in canonical order. See "Section manifest". |
| `defaultBranch` | Detected default branch. Omitted when not detected. |
| `remoteOwner` | Owner parsed from the `origin` remote URL. Omitted when not detected. |
| `ciScriptDrift` | One entry per CI script managed by `scaffold_ci`. Always present; may be empty. |
| `userConfigPath` | Absolute path of the user file (`$SDLC_USER_CONFIG`, else `~/.sdlc/local.toml`); `""` when no home directory is available. See "Personal settings values". |
| `localValues` | Merged personal settings per local setup section, with the source file of each key. See "Personal settings values". |
| `explanation` | Only with `explain`: `option`, `label`, `type`, `options`, `default`, `description`, `details`, `examples`, `configFile`, `configPath`, `consumedBy`. |
| `next` | Only with `explain`: tells the skill to show the explanation and ask the open question again. |

- With `explain`, the result holds only `ok`, `explanation` and `next`.
- An empty `options` list renders as `(none)`, never as a blank line.

#### Scenario: Fresh temp directory
- **WHEN** the tool runs in an empty directory that is not a git repository
- **THEN** `ok` is `true`
- **AND** `sections` is non-empty
- **AND** the first section has a non-empty `id`
- **AND** the first section has a non-empty `label`

#### Scenario: Explain one option
- **WHEN** the caller passes `{"explain":"ship.rebase"}`
- **THEN** `explanation.option` is `ship.rebase`
- **AND** `explanation.details` is non-empty
- **AND** `explanation.examples` has 1 to 3 entries
- **AND** `sections` is omitted

#### Scenario: Explain output has no personal settings
- **WHEN** the caller passes `{"explain":"ship.rebase"}`
- **THEN** `userConfigPath` and `localValues` are omitted

### Requirement: Section manifest
The tool SHALL return exactly 19 section descriptors, in this order: `version`, `ship`, `jira`, `review`, `received-review`, `commit`, `pr`, `github`, `pr-labels`, `review-dimensions`, `pr-template`, `plan-template`, `communication-style`, `plan-style`, `plan-tasks`, `plan-guardrails`, `execution-guardrails`, `openspec-block`, `automation`.

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
| `defaultTarget` | Default save target of a local section: `user` or `project` (table below); omitted for every other section. |
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
| `examples` | 1 to 3 entries, each `<TOML value> — <meaning>`. For `enum` and `multi-enum` fields, each value is one of `options`. |
| `min` / `max` | Numeric bounds; omitted when unset. |
| `whenStepInActiveSteps` | Ask the field only when this step is in `ship.steps`; omitted when unset. |

`delegatedTo` per section:

| `delegatedTo` | Section ids |
|---|---|
| (omitted) | `version`, `ship`, `jira`, `review`, `received-review`, `github`, `communication-style`, `plan-style`, `plan-tasks`, `automation` |
| `inline-commit-builder` | `commit` |
| `inline-pr-builder` | `pr` |
| `setup-pr-labels` | `pr-labels` |
| `setup-dimensions` | `review-dimensions` |
| `setup-pr-template` | `pr-template` |
| `setup-plan-template` | `plan-template` |
| `setup-guardrails` | `plan-guardrails` |
| `setup-execution-guardrails` | `execution-guardrails` |
| `setup-openspec` | `openspec-block` |

`defaultTarget` per section:

| `defaultTarget` | Section ids |
|---|---|
| `user` | `communication-style`, `plan-style`, `review`, `received-review`, `automation`, `github` |
| `project` | `ship` |
| (omitted) | every section of `.sdlc-v2/config.toml` and every delegated section |

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

#### Scenario: Every field has examples
- **WHEN** the caller reads any entry of any `fields[]` list
- **THEN** `examples` has 1 to 3 entries

#### Scenario: Default save targets
- **WHEN** the caller reads `sections`
- **THEN** the `communication-style` row has `defaultTarget: "user"`
- **AND** the `ship` row has `defaultTarget: "project"`
- **AND** the `jira` row has no `defaultTarget`

## ADDED Requirements

### Requirement: Personal settings values
Without `explain`, the tool SHALL return `localValues` with one entry per local setup section, keyed by section id, holding the merged values of the user file and `.sdlc-v2/local.toml` and the source file of each key.

- Entry: `{values, sources}`. `values` merges by key; the project file wins. `sources` maps each dotted key to `user` or `project`.
- A section with no keys in either file is `{values: {}, sources: {}}`.
#### Scenario: Key in both files
- **WHEN** the user file has `[style] audience = "functional"` and `tone = "direct"`
- **AND** `.sdlc-v2/local.toml` has `[style] audience = "technical"`
- **THEN** `localValues["communication-style"].values` is `{audience: "technical", tone: "direct"}`
- **AND** `localValues["communication-style"].sources` is `{audience: "project", tone: "user"}`

#### Scenario: Empty section
- **WHEN** neither file has a `[github]` section
- **THEN** `localValues.github` is `{values: {}, sources: {}}`

#### Scenario: Seven entries
- **WHEN** the tool runs in an empty directory
- **THEN** `localValues` has exactly 7 keys: `communication-style`, `plan-style`, `review`, `received-review`, `automation`, `github`, `ship`

#### Scenario: User file path
- **WHEN** `SDLC_USER_CONFIG` is `/tmp/u/local.toml`
- **THEN** `userConfigPath` is `/tmp/u/local.toml`

### Requirement: Personal settings read error
The tool SHALL return an `InfraError` with message `read personal settings: <err>` when the user file or `.sdlc-v2/local.toml` has bad TOML or is a directory; `<err>` names the file path.

- Suggestion: `Fix the TOML syntax in the file the error names, then retry setup_prepare. If the path comes from SDLC_USER_CONFIG, point it to a valid file or unset it.`

#### Scenario: Malformed user file
- **WHEN** the user file holds invalid TOML
- **THEN** the tool returns an `InfraError` whose message starts with `read personal settings:`
- **AND** the message names the user file path

#### Scenario: Malformed project file
- **WHEN** `.sdlc-v2/local.toml` holds invalid TOML
- **THEN** the tool returns an `InfraError` whose message starts with `read personal settings:`
- **AND** the message names `.sdlc-v2/local.toml`
