## ADDED Requirements

### Requirement: Staging of another plan is kept
The system SHALL refuse an `openspec_stage` call, before any write, when the staging directory of the change holds a `stage.json` whose `planPath` is not empty and differs from the `planPath` of the call after path cleaning. The call SHALL return a DomainError that names the other plan path, and SHALL write nothing. The message SHALL tell the caller to call again with that `planPath` when both paths name the same plan file.

#### Scenario: Another plan staged the same change
- **WHEN** the `stage.json` of change `add-widget` has `planPath` `/plans/a.md`
- **AND** an `openspec_stage` call for `add-widget` has `planPath` `/plans/b.md`
- **THEN** the call fails with a DomainError that names `/plans/a.md`
- **AND** the staging folder of `add-widget` keeps its files and its `stage.json`

#### Scenario: Same plan file under another path
- **WHEN** the `stage.json` of change `add-widget` has `planPath` `/plans/a.md`
- **AND** an `openspec_stage` call for `add-widget` has the relative `planPath` `plans/a.md`
- **THEN** the call fails with a DomainError that names `/plans/a.md`
- **AND** the message says to call again with `planPath` set to `/plans/a.md` when both paths name the same plan file

#### Scenario: Re-stage by the same plan
- **WHEN** the `stage.json` of change `add-widget` has `planPath` `/plans/a.md`
- **AND** an `openspec_stage` call for `add-widget` has `planPath` `/plans/a.md`
- **THEN** the call replaces the staging folder, as before

### Requirement: Staging with an unknown owner is replaced
The system SHALL skip the ownership check, and replace the staging directory, when the call has no `planPath`, or when the existing `stage.json` is missing, cannot be read, cannot be parsed, or has no `planPath`.

#### Scenario: Call with no plan path
- **WHEN** the `stage.json` of change `add-widget` has `planPath` `/plans/a.md`
- **AND** an `openspec_stage` call for `add-widget` has no `planPath`
- **THEN** the call replaces the staging folder, as before

#### Scenario: Manifest that cannot be parsed
- **WHEN** the `stage.json` of change `add-widget` is not valid JSON
- **AND** an `openspec_stage` call for `add-widget` has `planPath` `/plans/b.md`
- **THEN** the call replaces the staging folder, as before
