# Spec Delta

## MODIFIED Requirements

### Requirement: Deep merge with project precedence
The system SHALL merge the user file under `.sdlc-v2/local.toml`: tables merge key by key at every depth, the project value wins on a conflict, and a list or a scalar in the project file replaces the user value whole.

- The ship step tables `[ship.steps]` and `[ship.quick]` are tables, so they merge key by key. The project file wins for each step that it sets.

#### Scenario: Key in both files
- **WHEN** the user file has `[style] audience = "functional"` and `tone = "direct"`
- **AND** `.sdlc-v2/local.toml` has `[style] audience = "technical"`
- **THEN** the merged `[style]` is `audience = "technical"` and `tone = "direct"`

#### Scenario: Section only in the user file
- **WHEN** the user file has `[review] scope = "diff"`
- **AND** `.sdlc-v2/local.toml` has no `[review]` section
- **THEN** the merged `review.scope` is `diff`

#### Scenario: Nested table
- **WHEN** the user file has `[automation.report] enabled = false` and `format = "json"`
- **AND** `.sdlc-v2/local.toml` has `[automation.report] format = "md"`
- **THEN** the merged `automation.report` is `enabled = false` and `format = "md"`

#### Scenario: List replaced whole
- **WHEN** the user file has `[ship] awaitRemoteReviewers = ["copilot", "coderabbit"]`
- **AND** `.sdlc-v2/local.toml` has `[ship] awaitRemoteReviewers = ["copilot"]`
- **THEN** the merged `ship.awaitRemoteReviewers` is `["copilot"]`

#### Scenario: Step tables merge per key
- **WHEN** the user file has `[ship.steps]` with `await-remote-review = true` and `harden = true`
- **AND** `.sdlc-v2/local.toml` has `[ship.steps]` with `harden = false`
- **THEN** the merged `ship.steps` has `await-remote-review = true` and `harden = false`

### Requirement: Messages name both files
A user-facing error or warning about a `local.toml` section SHALL name both files as `.sdlc-v2/local.toml (or ~/.sdlc/local.toml)`.

- This covers the `[review]` read error and the old-key `maxDimensions` error of `review_prepare`, the `[github]` warnings of `pr_prepare`, and the `reviewThreshold` error and the old step-list errors of `ship_prepare`.
- The old-key error of `review_prepare` names the new key `maxParallelDimensions`.

#### Scenario: Invalid review threshold
- **WHEN** the merged `[ship] reviewThreshold` is `urgent`
- **THEN** the `ship_prepare` error ends with `in [ship] of .sdlc-v2/local.toml (or ~/.sdlc/local.toml)`

#### Scenario: Unreadable github section
- **WHEN** the `[github]` section cannot be read
- **THEN** the `pr_prepare` warning starts with `.sdlc-v2/local.toml (or ~/.sdlc/local.toml) [github] section unreadable:`

#### Scenario: Old review key
- **WHEN** the merged `[review]` section has `maxDimensions = 8`
- **THEN** the `review_prepare` error starts with `[review] maxDimensions in .sdlc-v2/local.toml (or ~/.sdlc/local.toml) was renamed to maxParallelDimensions.`

#### Scenario: Old ship step list
- **WHEN** the merged `[ship]` section has `steps = ["execute", "commit"]`
- **THEN** the `ship_prepare` error starts with `ship.steps in .sdlc-v2/local.toml (or ~/.sdlc/local.toml) uses the old list shape`
