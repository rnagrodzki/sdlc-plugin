# Spec Delta

## Purpose

MCP tool `openspec_save` saves the OpenSpec change that the plan skill staged into `openspec/changes/` on its own branch, so the specs can ship as a separate PR before the code.

## ADDED Requirements

### Requirement: Registration
The tool SHALL be registered as `openspec_save` with input `planPath` (absolute path) and annotations `ReadOnly: false`, `Destructive: true`, `Idempotent: true`, `OpenWorld: false`. Its output SHALL have `change`, `branch`, `branchCreated`, `materialized`, `refsStamped`, `stagedFiles`, `summary`, and `next`.

#### Scenario: Client lists tools
- **WHEN** an MCP client lists the server's tools
- **THEN** `openspec_save` is present with `Destructive: true`, `Idempotent: true`

### Requirement: Branch rule
The tool SHALL run on the default branch or on `openspec/<change>` only. On the default branch it SHALL create `openspec/<change>` with `git switch -c`. Every check SHALL run before the branch switch, and a failed check SHALL change nothing. A git failure SHALL return an InfraError with a Suggestion.

#### Scenario: Default branch other than main
- **WHEN** `origin/HEAD` points to `develop` and the current branch is `develop`
- **THEN** the tool creates branch `openspec/<change>`

#### Scenario: Branch already exists
- **WHEN** `openspec/<change>` exists and is not the current branch
- **THEN** the tool returns a DomainError whose Suggestion says to switch to it

#### Scenario: Feature branch
- **WHEN** the current branch is `feat/x`
- **THEN** the tool returns a DomainError whose Suggestion says to switch to the default branch

#### Scenario: Unrelated change
- **WHEN** `internal/x.go` has a tracked change
- **THEN** the tool returns a DomainError that lists `internal/x.go`
- **AND** the branch does not change

### Requirement: Save and header rewrite
The tool SHALL materialize the staged change, stamp task references in `tasks.md`, stage the change folder, and rewrite the plan line `**OpenSpec-Staging:**` to `**OpenSpec-Saved:** openspec/changes/<change>/ (branch openspec/<change>)`. `stagedFiles` SHALL list the staged paths of the change folder on every path.

#### Scenario: First save
- **WHEN** the plan has `**OpenSpec-Staging:** .sdlc-v2/openspec-staging/demo/` on `main`
- **THEN** the branch is `openspec/demo`
- **AND** the plan has `**OpenSpec-Saved:**` and no Staging line
- **AND** `materialized` is `created`

#### Scenario: Second call
- **WHEN** the plan has `**OpenSpec-Saved:**` and no Staging line
- **THEN** `materialized` is `already`

#### Scenario: Both header lines
- **WHEN** the plan has a Staging line and a Saved line
- **THEN** the tool returns a DomainError

#### Scenario: Plan write fails
- **WHEN** the plan file cannot be written after the save
- **THEN** the tool returns an InfraError whose Suggestion says to call `openspec_save` again
