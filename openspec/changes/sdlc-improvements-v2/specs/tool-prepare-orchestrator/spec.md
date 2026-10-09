# Spec Delta

## MODIFIED Requirements

### Requirement: Output and manifest file
The tool SHALL write the manifest to `manifest.json` in a new OS temp directory on every successful call and return its path; it SHALL NOT delete the file.

| Field | Meaning |
|---|---|
| `manifestPath` | Absolute path of the written `manifest.json` |
| `mode` | The mode that ran |
| `customInstructions` | `harden` mode only: the `[harden.instructions]` lists, one list for each proposal surface |
| `next` | The step after the call, in both modes |

- The caller removes the manifest when done.

#### Scenario: Fresh manifest per call
- **WHEN** the tool is called twice with valid `error_report` input
- **THEN** each call returns a different `manifestPath`

#### Scenario: Next text
- **WHEN** the tool runs in `error_report` mode
- **THEN** `next` is `Dispatch sdlc:error-report-orchestrator with manifestPath.`

## ADDED Requirements

### Requirement: harden custom instructions
In `harden` mode the tool SHALL read `[harden.instructions]` from `.sdlc-v2/config.toml`, SHALL put the four lists into the manifest and the output as `customInstructions`, and SHALL fail with a DomainError for an invalid list.

| Key | Default | Limit |
|---|---|---|
| `plan-guardrails`, `execute-guardrails`, `review-dimensions`, `copilot-instructions` | `[]` | 10 items, 1024 characters each |

#### Scenario: No section
- **WHEN** the config has no `[harden]` section
- **THEN** `customInstructions` has the four keys, each with `[]`

#### Scenario: Unknown key
- **WHEN** `[harden.instructions]` has the key `x`
- **THEN** the tool returns a DomainError `harden.instructions has unknown key "x"`
- **AND** the Suggestion lists the four valid keys
