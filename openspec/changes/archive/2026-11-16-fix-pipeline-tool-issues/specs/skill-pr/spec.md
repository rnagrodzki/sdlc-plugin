# Spec Delta

## ADDED Requirements

### Requirement: Approved draft published unchanged
When the invocation prompt holds a valid `Approved PR draft (publish unchanged):` block (a title line and a non-empty body), the pr skill SHALL skip the draft, critique and improve steps and SHALL send that title and body to `pr_apply` unchanged.

#### Scenario: Valid block
- **WHEN** the prompt holds the block with `title: fix: x` and a body
- **THEN** `pr_apply` gets the title `fix: x` and that body

#### Scenario: Invalid block
- **WHEN** the block has no title line
- **THEN** the skill drafts the description as it does with no block

#### Scenario: Attribution line
- **WHEN** the approved body holds an AI attribution line
- **THEN** `pr_apply` still removes it
