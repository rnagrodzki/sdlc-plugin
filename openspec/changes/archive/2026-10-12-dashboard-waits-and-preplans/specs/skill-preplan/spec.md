# Spec Delta

## MODIFIED Requirements

### Requirement: Topic file start
The `/sdlc:preplan` skill SHALL get the plan guardrails and the topic file path from `plan_support` action `preplan_context`, and SHALL NOT start a plan run.

#### Scenario: First run for a topic
- **WHEN** the user runs `/sdlc:preplan auth flow` on 2026-10-12 (UTC) and `.sdlc-v2/preplan/` holds no `auth-flow.md` and no `YYYY-MM-DD-auth-flow.md`
- **THEN** the skill calls `plan_support` with `action: "preplan_context"` and `topic: "auth flow"`
- **AND** `.sdlc-v2/preplan/2026-10-12-auth-flow.md` exists with status `in progress` and a `**Started:**` line
- **AND** no new `plan-*` state file exists in `.sdlc-v2/runs/`

#### Scenario: Run again for a topic
- **WHEN** the user runs `/sdlc:preplan auth flow` and `.sdlc-v2/preplan/auth-flow.md` exists from before dated names
- **THEN** the skill continues `auth-flow.md`
- **AND** no dated file is created

#### Scenario: Tool error
- **WHEN** `preplan_context` returns an error
- **THEN** the skill prints the error and stops

#### Scenario: Two matching topic files
- **WHEN** `preplan_context` returns the error for two topic files that match
- **THEN** the skill prints the error's `## Do this` text and stops
