# Spec Delta

## MODIFIED Requirements

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

## ADDED Requirements

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
