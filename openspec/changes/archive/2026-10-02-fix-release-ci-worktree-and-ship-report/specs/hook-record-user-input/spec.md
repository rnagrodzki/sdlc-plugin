# Spec Delta

## ADDED Requirements

### Requirement: Typed prompts recorded during an active run
The `record-user-input` hook (`UserPromptSubmit`) SHALL append each prompt the user types to `.sdlc-v2/evidence/user-inputs.jsonl` under the main worktree root when a ship or execute run is active on the current branch, and SHALL write nothing otherwise.

- The prompt text is read from `prompt_text`, else `prompt`. A blank prompt is not recorded.
- Each line is `{ts, pipeline, step?, wave?, branch, text}`; `ts` is RFC3339 UTC.
- `text` is redacted (bearer tokens, JWTs, cookies, cloud IDs, emails) and cut to 2000 characters with `…` appended when cut.
- The file uses the same size-bounded append as `cli-executions.jsonl`.

| State for the branch | Recorded | pipeline | step / wave |
|---|---|---|---|
| ship, advancing | yes | `ship` | advancing step name |
| ship, not advancing, a step `failed` | yes | `ship` | first failed step name |
| ship, not advancing, no step failed | no | — | — |
| execute, `runStatus` not `completed` | yes | `execute` | latest recorded wave number |
| execute, `runStatus: "completed"` | no | — | — |
| none | no | — | — |

#### Scenario: Prompt during a ship step
- **WHEN** the ship step `review` is `in_progress` and the user types `skip the low findings`
- **THEN** one line is appended with `pipeline:"ship"`, `step:"review"` and `text:"skip the low findings"`

#### Scenario: Prompt after a failed step
- **WHEN** the ship step `pr` is `failed` and the user types a correction
- **THEN** the line has `step:"pr"`

#### Scenario: No active run
- **WHEN** no ship or execute state exists for the branch, or the ship run has completed every step
- **THEN** nothing is written and no file is created

#### Scenario: Secret in a prompt
- **WHEN** the prompt contains `Bearer abc.def`
- **THEN** the stored text contains `Bearer [REDACTED]` and not `abc.def`

### Requirement: Hook stays silent
The hook SHALL exit 0 and write nothing to stdout on every path, including errors, so nothing is added to Claude's context.

#### Scenario: Recorded prompt
- **WHEN** a prompt is recorded
- **THEN** the hook exit code is 0 and stdout is empty

#### Scenario: Evidence write fails
- **WHEN** the evidence directory cannot be written
- **THEN** the hook exit code is 0, stdout is empty, and the prompt is submitted as usual
