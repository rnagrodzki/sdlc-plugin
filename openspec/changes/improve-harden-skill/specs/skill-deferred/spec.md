# Spec Delta

## ADDED Requirements

### Requirement: Pending rule edits are not filed
In triage, the skill SHALL NOT draft or file a GitHub issue for an item with source `harden-pending`. It SHALL print the harden command that applies the edits and SHALL leave the item open.

#### Scenario: Harden-pending item picked
- **WHEN** the user picks item `harden-pending-plan` in triage
- **THEN** the skill prints `/sdlc:harden --failure-text "Pending rule edits: harden-pending-plan" --skill deferred`
- **AND** it makes no `gh issue create` call
- **AND** the item stays open
