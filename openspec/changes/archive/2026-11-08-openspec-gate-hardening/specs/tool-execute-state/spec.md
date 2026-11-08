# Spec Delta

## ADDED Requirements

### Requirement: Init refuses an unauthored OpenSpec plan
`execute_state` init SHALL return DomainError for a plan that `ship_prepare` refuses as unauthored (a Create line with no Staging or Saved line, or an Appendix that starts with `[TBD`). The error names the plan file and the change, the Suggestion holds the fix, and no state file is written.

#### Scenario: Create line only
- **WHEN** init gets a plan with `**OpenSpec-Create:** x` and no Staging or Saved line
- **THEN** init returns DomainError with the Suggestion `Run /sdlc:plan on the plan again to finish the OpenSpec authoring, or remove the **OpenSpec-Create:** and **Source:** lines to ship without OpenSpec.`
- **AND** no execute state file is written

#### Scenario: Staged plan passes
- **WHEN** the plan has an `**OpenSpec-Staging:**` line and a filled Appendix
- **THEN** init does not return the header error
