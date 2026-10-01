# Spec Delta

## ADDED Requirements

### Requirement: OpenSpec materialize at ship start
After validation passes and before the state file is written, the tool SHALL materialize a staged OpenSpec change when `planFile` has an `**OpenSpec-Staging:**` header, as defined by the `openspec-staging` capability, and SHALL return the outcome in `openspec: {change, materialized}`.

- `dryRun: true` reports `openspec.materialized: "dry-run"` and writes nothing.
- A materialize error is added to `errors`, so no state file is created.

#### Scenario: Staged plan
- **WHEN** `ship_prepare({planFile:"<plan>", hasPlan:true})` runs and the plan stages `add-widget`
- **THEN** `openspec/changes/add-widget/` exists and is staged in git
- **AND** the output has `openspec: {change:"add-widget", materialized:"created"}`

#### Scenario: Materialize fails
- **WHEN** `openspec validate add-widget --strict` fails during materialize
- **THEN** `errors` contains the CLI output
- **AND** no ship state file is created

### Requirement: OpenSpec change from the plan header
When the `openspecChange` input is empty and `planFile` has `**Source:** openspec/changes/<name>/`, the tool SHALL set `flags.openspecChange` to `<name>` and `sources.openspecChange` to `"plan"`.

- A non-empty `openspecChange` input wins, with `sources.openspecChange` `"cli"`.
- When the input and the plan header name different changes, `warnings` gets `openspecChange "<input>" differs from plan Source "<name>"; using "<input>"`.

#### Scenario: Name from the plan
- **WHEN** the call has no `openspecChange` and the plan's `**Source:**` is `openspec/changes/add-widget/`
- **THEN** `flags.openspecChange` is `"add-widget"`
- **AND** `sources.openspecChange` is `"plan"`
