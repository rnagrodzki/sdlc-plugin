# Spec Delta

## ADDED Requirements

### Requirement: PF15 OpenSpec handoff rule
`validate` action `plan_format` with `handoff: true` SHALL add PF15. PF15 returns one error finding when the plan has a Create line and no Staging line, when the Appendix starts with `[TBD`, or when the staging folder has no readable `stage.json`, no `validatedAt`, or a missing staged file. A Saved line skips the staging cases. `handoff` with another action SHALL return DomainError.

#### Scenario: Create line at the handoff
- **WHEN** `plan_format` runs with `handoff: true` on a plan with `**OpenSpec-Create:** a` and no Staging line
- **THEN** the findings hold `PF15: OpenSpec change "a" is not authored: the plan has **OpenSpec-Create:** and no **OpenSpec-Staging:** line`

#### Scenario: Not validated
- **WHEN** the staging `stage.json` of `a` has no `validatedAt`
- **THEN** the findings hold `PF15: staging of "a" is not validated (stage.json has no validatedAt)`

#### Scenario: No handoff
- **WHEN** `plan_format` runs without `handoff`
- **THEN** the findings hold no PF15 finding

#### Scenario: Other action
- **WHEN** `validate` runs action `plan_style` with `handoff: true`
- **THEN** it returns DomainError `handoff applies only to plan_format`
