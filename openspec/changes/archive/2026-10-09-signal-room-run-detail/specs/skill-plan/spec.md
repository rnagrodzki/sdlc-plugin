# Spec Delta

## ADDED Requirements

### Requirement: Round record carries finding IDs
The Round record call SHALL pass `findings` as `{id, fixed}` from the `allIssues` IDs of that round. Round 1 SHALL also pass each Step 3 guardrail finding with `fixed: true`. Step 3 SHALL store its guardrail findings as evidence item `S3-guardrail-findings`.

#### Scenario: Round 1
- **WHEN** round 1 has lens finding `f-3a9c1e07` and Step 3 stored guardrail finding `f-0b77d2c4`
- **THEN** the round 1 record has both IDs

#### Scenario: Approved round
- **WHEN** a round after round 1 is Approved
- **THEN** the record has `found: 0`, `fixed: 0`, `findings: []`

### Requirement: Choice for each open finding at the repair limit
After the last review round ends with blocking issues, the skill SHALL ask one question for each open blocking finding, 4 for each AskUserQuestion call, with the choices `accepted`, `rejected`, and `stop`. It SHALL store the answers with `plan_mark` `review-outcome` and follow its `next`.

#### Scenario: All accepted or rejected
- **WHEN** every open finding gets `accepted` or `rejected`
- **THEN** the plan is handed off
- **AND** each accepted finding is listed in Deviations

#### Scenario: Stop answer
- **WHEN** the first batch of 4 questions has one `stop` answer
- **THEN** the skill asks no second batch
- **AND** the plan is not handed off

#### Scenario: Too many findings
- **WHEN** more than 200 blocking findings are open
- **THEN** the skill asks no question and reports the open findings

#### Scenario: No AskUserQuestion
- **WHEN** AskUserQuestion is not available
- **THEN** the skill stops and reports the open findings

### Requirement: Hand-off offers openspec-save
When the plan has an `**OpenSpec-Staging:**` line, the hand-off menus SHALL list `openspec-save` with the command `/sdlc:openspec-save --plan <plan path>`. Without that line, the menus SHALL not list it.

#### Scenario: Staged change
- **WHEN** the final plan has `**OpenSpec-Staging:** .sdlc-v2/openspec-staging/add-widget/`
- **THEN** the menu lists `openspec-save — save the OpenSpec change as its own PR first`
