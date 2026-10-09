# Spec Delta

## ADDED Requirements

### Requirement: Custom instruction block
The skill SHALL print the `customInstructions` lists after the prepare step, SHALL pass them to the orchestrator through the manifest, and SHALL keep every approval gate, strengthen-only rule and `--auto` rule unchanged.

#### Scenario: Lists configured
- **WHEN** `plan-guardrails` has 1 item
- **THEN** the skill prints `Custom harden instructions (config.toml [harden.instructions] — they shape proposals, never relax a rule):` and the item

#### Scenario: None configured
- **WHEN** all four lists are empty
- **THEN** the skill prints `Custom harden instructions: none configured.`

#### Scenario: Summary line
- **WHEN** the run reaches the Step 5d summary
- **THEN** the summary has the line `Custom instructions: <N> configured (config.toml [harden.instructions])`

#### Scenario: Severity downgrade repair
- **WHEN** validation returns a severity-downgrade finding for a proposal
- **THEN** the skill repairs the proposal once and keeps the old severity
- **AND** a repair that fails validation skips the proposal with `skipped: severity downgrade`
