# Spec Delta

## MODIFIED Requirements

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
| `type` | One of `boolean`, `enum`, `multi-enum`, `multi-select`, `flag-set`, `string`, `number`, `list`. |
| `options` | Allowed choices; omitted when empty. |
| `default` | Default value; omitted when unset. |
| `description` | Helper text. |
| `examples` | 1 to 3 entries, each `<TOML value> — <meaning>`. For `enum` and `multi-enum` fields, each value is one of `options`. |
| `min` / `max` | Numeric bounds; omitted when unset. |
| `whenStepInActiveSteps` | Ask the field only when this step is one of the selected names of the `ship.steps` answer; omitted when unset. |

- The `ship` fields `steps` and `quick` have type `flag-set`. Their `options` are the ship step list in the fixed pipeline order: `execute`, `commit`, `review`, `verify-openspec`, `archive-openspec`, `harden`, `pr`, `verify-pipeline`, `await-remote-review`, `learnings-commit`.
- Every other `multi-select` field stays `multi-select`.
- The `steps` field `description` names the steps in the same fixed order.
- The `review` section has the field `maxParallelDimensions` and no field `maxDimensions`.

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

#### Scenario: Ship step fields are flag sets
- **WHEN** the caller reads the `steps` and `quick` fields of the `ship` row
- **THEN** each field has `type: "flag-set"`
- **AND** each `options` list is `["execute","commit","review","verify-openspec","archive-openspec","harden","pr","verify-pipeline","await-remote-review","learnings-commit"]`

#### Scenario: Review parallel limit field
- **WHEN** the caller reads the `review` row
- **THEN** it has a field `maxParallelDimensions` with `type: "number"`, `min` `1` and `default` `8`
- **AND** it has no field `maxDimensions`
