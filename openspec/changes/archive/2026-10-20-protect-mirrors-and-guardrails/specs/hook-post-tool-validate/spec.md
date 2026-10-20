# Spec Delta

## ADDED Requirements

### Requirement: Dimension files trigger the dimensions validator
After an Edit or Write of a file `<name>.md` in `.claude/review-dimensions/`, `.sdlc-v2/review-dimensions/` or `.sdlc/review-dimensions/`, the `post-tool-validate` hook SHALL run the dimensions validator on the dimension folder and SHALL block the edit when it reports a finding. A `.yaml` or `.yml` file in those folders SHALL NOT trigger the hook.

#### Scenario: Broken dimension file
- **WHEN** the agent edits `.sdlc-v2/review-dimensions/bad.md`, which has no frontmatter
- **THEN** the hook returns `decision: block`
- **AND** the reason holds the finding `D1`

#### Scenario: Valid dimension folder
- **WHEN** the agent edits a dimension file and the dimensions validator reports no finding
- **THEN** the hook output is silent

#### Scenario: YAML file
- **WHEN** the agent edits `.sdlc-v2/review-dimensions/security.yaml`
- **THEN** the hook output is silent

### Requirement: config.toml triggers the guardrails validator
After an Edit or Write of `<root>/.sdlc-v2/config.toml`, the hook SHALL run the guardrails validator for the sections `plan` and `execute` under `<root>`, the folder two levels above the edited file. The hook SHALL block the edit when the validator reports a finding, and each finding SHALL name its section and the file.

- A validator error, for example invalid TOML, gives silent output.

#### Scenario: Invalid guardrail
- **WHEN** the agent edits `config.toml` so that `[execute.guardrails.no-desc]` has no `description`
- **THEN** the hook returns `decision: block`
- **AND** the reason holds `[execute.guardrails] no-desc: description is missing` and the path of `config.toml`

#### Scenario: Valid guardrails
- **WHEN** both guardrail sections pass the validator, or `config.toml` has no guardrail table
- **THEN** the hook output is silent

#### Scenario: Root of the edited file
- **WHEN** the process folder differs from the project folder that holds the edited `config.toml`
- **THEN** the hook checks the project folder of the edited file

#### Scenario: Other config file
- **WHEN** the agent edits `.sdlc-v2/local.toml`
- **THEN** the hook output is silent

#### Scenario: Invalid TOML
- **WHEN** the edited `config.toml` is not valid TOML
- **THEN** the hook output is silent

### Requirement: A mirror without a valid marker line gets a note
After an Edit or Write of `.github/instructions/<name>.instructions.md`, the hook SHALL add a note and SHALL NOT block when the file has no valid `sdlc:generated` marker line and the dimension file `.sdlc-v2/review-dimensions/<name>.md` exists. The note names the dimension file and `overwrite: true`. In every other case the hook SHALL be silent.

#### Scenario: Hand-edited mirror
- **WHEN** the agent edits a mirror so that its marker hash no longer matches
- **AND** the dimension file of the same name exists
- **THEN** the hook adds context that names the dimension file and `overwrite: true`
- **AND** the output holds no `decision` key

#### Scenario: Valid marker line
- **WHEN** the edited mirror ends with a marker line whose hash matches the text above it
- **THEN** the hook output is silent

#### Scenario: No dimension file
- **WHEN** the edited file has no valid marker line
- **AND** no dimension file of the same name exists
- **THEN** the hook output is silent

#### Scenario: Unreadable mirror
- **WHEN** the hook cannot read the edited mirror
- **THEN** the hook output is silent
