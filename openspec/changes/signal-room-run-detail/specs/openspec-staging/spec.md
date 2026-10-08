# Spec Delta

## ADDED Requirements

### Requirement: Saved header stops a second save
A plan whose header has `**OpenSpec-Saved:**` and no `**OpenSpec-Staging:**` line SHALL materialize nothing at ship or execute start. `openspec_save` writes this header after it saves the change.

#### Scenario: Ship after openspec-save
- **WHEN** `/sdlc:openspec-save` saved the change and the user starts ship
- **THEN** `ship_prepare` materializes nothing
- **AND** the change is not saved again
