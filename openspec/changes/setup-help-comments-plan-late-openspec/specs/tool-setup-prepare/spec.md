## MODIFIED Requirements

### Requirement: Input fields
The tool SHALL accept the input fields below.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `skipConfigCheck` | bool | optional at call time; missing = `false` | boolean, e.g. `false` | Skip the config-migration check. `needsMigration` is then always `false`. |
| `explain` | string | optional | plain text `<sectionId>.<fieldName>`, e.g. `ship.rebase` | Return only the explanation of that option. Split at the first `.`: the left part is the section id, the rest is the field name. |

| `explain` input | Error | Suggestion |
|---|---|---|
| no `.`, or an empty part | `DomainError` `setup_prepare: explain "<v>" is not <sectionId>.<fieldName>` | `Pass a value such as "ship.rebase".` |
| unknown section | `DomainError` `setup_prepare: unknown section "<s>"; valid: <ids>` | `Use a section id from sections[].id.` |
| section has no fields | `DomainError` `setup_prepare: section "<s>" has no fields; it runs the <delegatedTo> sub-flow` | `Explain from that sub-flow file instead.` |
| unknown field | `DomainError` `setup_prepare: section "<s>" has no field "<f>"; valid: <names>` | `Use a name from sections[].fields[].name.` |

#### Scenario: Empty input
- **WHEN** the caller passes `{}`
- **THEN** the tool runs the config-migration check
- **AND** returns `ok: true`

#### Scenario: Explain without a dot
- **WHEN** the caller passes `{"explain":"ship"}`
- **THEN** the tool returns a `DomainError` with message `setup_prepare: explain "ship" is not <sectionId>.<fieldName>`

#### Scenario: Explain a delegated section
- **WHEN** the caller passes `{"explain":"commit.style"}` and the `commit` section has no fields
- **THEN** the tool returns a `DomainError` that names the `inline-commit-builder` sub-flow

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
| `examples` | 1 to 3 entries, each `<TOML value> — <meaning>`. For `enum` and `multi-enum` fields, each value is one of `options`. |
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

#### Scenario: Every field has examples
- **WHEN** the caller reads any entry of any `fields[]` list
- **THEN** `examples` has 1 to 3 entries
