# Spec Delta

## MODIFIED Requirements

### Requirement: Pre-approval validation on a temp copy
After staging, the system SHALL copy `openspec/config.yaml`, the staged change, and the current `openspec/specs/<capability>/spec.md` of each capability that the change has a delta for into a new temp directory, run `openspec validate <change> --strict` there, return the CLI exit code and output, and record `validatedAt` in `stage.json` only when the exit code is 0.

- A capability with no current spec copies nothing.
- A current spec over 1 MiB, or one that cannot be read, stops the stage call with an error and no `validatedAt`.

#### Scenario: Valid staged change
- **WHEN** the staged change passes `openspec validate --strict` in the temp copy
- **THEN** the result reports `valid: true`
- **AND** `stage.json` has `validatedAt`

#### Scenario: Invalid staged change
- **WHEN** validation fails in the temp copy
- **THEN** the result reports `valid: false` with the CLI output
- **AND** `stage.json` has no `validatedAt`
- **AND** the repository is unchanged

#### Scenario: Modified delta drops a current scenario
- **WHEN** the change has a MODIFIED delta for `tool-plan-prepare` that leaves out a scenario of the current `openspec/specs/tool-plan-prepare/spec.md`
- **THEN** the result reports `valid: false`
- **AND** the output has `omits scenario(s) the current spec still has`
