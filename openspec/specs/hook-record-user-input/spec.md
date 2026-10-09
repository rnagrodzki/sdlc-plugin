# hook-record-user-input Specification

## Purpose
The `record-user-input` hook records prompts the user types while a ship or execute run is active, so the ship run report can show when and why a run changed course.

## Requirements

### Requirement: Typed prompts recorded during an active run
The `record-user-input` hook (`UserPromptSubmit`) SHALL append each prompt the user types to `.sdlc-v2/evidence/user-inputs.jsonl` under the main worktree root when a ship or execute run is active on the current branch, and SHALL write nothing otherwise. A turn that the editor or the agent runtime injects is not a typed prompt and SHALL NOT be recorded.

- The prompt text is read from `prompt_text`, else `prompt`. A blank prompt is not recorded.
- Each line is `{ts, pipeline, step?, wave?, branch, text, kind, sessionId}`; `ts` is RFC3339 UTC; `kind` is `prompt`. A line with no `kind` (written before this field existed) is read as `prompt`.
- `sessionId` is the hook `session_id`. The key is always present: an empty id writes `"sessionId":""`. A line with no `sessionId` (written before this field existed) still parses.
- `text` is redacted (bearer tokens, JWTs, cookies, cloud IDs, emails) and cut to 2000 characters with `…` appended when cut.
- The file uses the same size-bounded append as `cli-executions.jsonl`.
- Injected-turn filter, applied before the active-run lookup:

| Prompt text | Recorded `text` |
|---|---|
| First non-blank text starts with `<task-notification>`, `<local-command-stdout>`, `<local-command-stderr>` or `<local-command-caveat>` | nothing (turn dropped) |
| Holds `<ide_opened_file>…</ide_opened_file>`, `<ide_selection>…</ide_selection>` or `<system-reminder>…</system-reminder>` envelopes (multi-line, anywhere in the text) | the text with each envelope removed, trimmed; nothing when no text remains |
| A slash-command envelope `<command-name>/<name></command-name><command-args><args></command-args>` | `/<name> <args>` |
| Any other text, including an unknown tag | the text unchanged |

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
- **THEN** one line is appended with `pipeline:"ship"`, `step:"review"`, `text:"skip the low findings"` and `kind:"prompt"`

#### Scenario: Session id on the line
- **WHEN** the ship step `review` is `in_progress`
- **AND** the hook payload has `session_id:"3f2c"`
- **THEN** the appended line has `"sessionId":"3f2c"`

#### Scenario: Empty session id
- **WHEN** a ship run is active
- **AND** the hook payload has no `session_id`
- **THEN** the appended line has `"sessionId":""`

#### Scenario: Prompt after a failed step
- **WHEN** the ship step `pr` is `failed` and the user types a correction
- **THEN** the line has `step:"pr"`

#### Scenario: No active run
- **WHEN** no ship or execute state exists for the branch, or the ship run has completed every step
- **THEN** nothing is written and no file is created

#### Scenario: Secret in a prompt
- **WHEN** the prompt contains `Bearer abc.def`
- **THEN** the stored text contains `Bearer [REDACTED]` and not `abc.def`

#### Scenario: Agent notification dropped
- **WHEN** the ship step `execute` is `in_progress`
- **AND** the prompt text starts with `<task-notification>`
- **THEN** nothing is written

#### Scenario: Editor envelope removed
- **WHEN** the ship step `review` is `in_progress`
- **AND** the prompt text is `<ide_opened_file>The user opened x</ide_opened_file>` then a new line, then `why is it slow?`
- **THEN** the appended line has `text:"why is it slow?"`

#### Scenario: Envelope only
- **WHEN** the prompt text is only `<system-reminder>…</system-reminder>`
- **THEN** nothing is written

#### Scenario: Slash command
- **WHEN** the ship step `review` is `in_progress`
- **AND** the prompt text is `<command-name>/sdlc:ship</command-name><command-args>--auto</command-args>`
- **THEN** the appended line has `text:"/sdlc:ship --auto"`

### Requirement: Hook stays silent
The hook SHALL exit 0 and write nothing to stdout on every path, including errors, so nothing is added to Claude's context.

#### Scenario: Recorded prompt
- **WHEN** a prompt is recorded
- **THEN** the hook exit code is 0 and stdout is empty

#### Scenario: Evidence write fails
- **WHEN** the evidence directory cannot be written
- **THEN** the hook exit code is 0, stdout is empty, and the prompt is submitted as usual

### Requirement: Question answers recorded during an active run
The `record-user-answer` hook (`PostToolUse`, matcher `AskUserQuestion`) SHALL append one line per answered question to `.sdlc-v2/evidence/user-inputs.jsonl` when a ship or execute run is active, with the same run rules as the `record-user-input` hook.

- Each line has `kind:"answer"` and `text` `<header>: <answer>`; `<header>` is the question's `header`, else its `question` text.
- `text` is redacted and cut like a prompt.

#### Scenario: One answered question
- **WHEN** the ship step `review` is `in_progress`
- **AND** `tool_input.questions[0]` is `{header:"OpenSpec", question:"How …?"}`
- **AND** `tool_input.answers` is `{"How …?":"Skip OpenSpec"}`
- **THEN** one line is appended with `kind:"answer"`, `step:"review"` and `text:"OpenSpec: Skip OpenSpec"`

#### Scenario: Question with no header
- **WHEN** a ship run is active
- **AND** the answered question has no `header` and the `question` text `Which base?`
- **AND** the answer is `main`
- **THEN** the appended line has `text:"Which base?: main"`

### Requirement: Answer source
The `record-user-answer` hook SHALL read answers from `tool_input.answers`, else from `tool_response.answers`, and SHALL write nothing when neither holds answers or no run is active.

#### Scenario: Answers only in the tool response
- **WHEN** a ship run is active
- **AND** `tool_input` has no `answers`
- **AND** `tool_response.answers` holds one answer
- **THEN** one `kind:"answer"` line is appended

#### Scenario: No answers
- **WHEN** neither `tool_input` nor `tool_response` has `answers`
- **THEN** nothing is written

#### Scenario: No active run
- **WHEN** no ship or execute run is active on the branch
- **AND** the payload has answers
- **THEN** nothing is written and no file is created

### Requirement: Answer hook stays silent
The `record-user-answer` hook SHALL exit 0 and write nothing to stdout on every path, including errors, so nothing is added to Claude's context.

#### Scenario: Recorded answer
- **WHEN** an answer is recorded
- **THEN** the hook exit code is 0 and stdout is empty

#### Scenario: Evidence write fails
- **WHEN** the evidence directory cannot be written
- **THEN** the hook exit code is 0 and stdout is empty

### Requirement: Answer closes the question wait
The `record-user-answer` hook SHALL delete the question wait record of its tool use id before any other check. With no tool use id, it SHALL delete every question record of the session.

#### Scenario: Answer arrives
- **WHEN** the user answers the question with tool use id `toolu_9`
- **THEN** the file `s1-toolu_9.json` does not exist

#### Scenario: No tool use id
- **WHEN** the answer event has no `tool_use_id`
- **THEN** no question record of the session exists

### Requirement: Prompt closes every wait
The `record-user-input` hook SHALL delete every wait record of the session when it keeps the prompt. An injected turn that the hook drops SHALL delete nothing.

#### Scenario: Typed prompt
- **WHEN** the user types a prompt in session `s1`
- **THEN** no wait record of `s1` exists

#### Scenario: Injected turn
- **WHEN** the prompt is a `<task-notification>` turn
- **THEN** the wait records of the session stay
