# hook-attention-records Specification

## Purpose
Records each open wait for a user answer (a question or a permission prompt) as a file, so the dashboard can show which runs wait for a person.

## Requirements

### Requirement: Wait record file
The system SHALL store each open wait as one JSON file under `.sdlc-v2/evidence/attention/` of the main worktree, with `version`, `kind` (`question` or `permission`), `sessionId`, `toolUseId` (question only), `branch`, `header`, `text`, and `askedAt` (RFC3339 UTC). `header` and `text` SHALL be redacted and capped at 120 characters.

#### Scenario: Question record
- **WHEN** session `s1` asks a question with tool use id `toolu_9` and header `Guardrail`
- **THEN** the file `.sdlc-v2/evidence/attention/s1-toolu_9.json` exists with `kind` `question` and `header` `Guardrail`

#### Scenario: Long text
- **WHEN** the question text has 300 characters
- **THEN** the stored `text` has at most 120 characters plus `…`

### Requirement: Question wait write
The `block-askuserquestion-auto` hook SHALL write a question record on every path that allows the question. An auto ship run that gets the deny output SHALL get no record. A write error SHALL not change the hook output or exit code.

#### Scenario: Auto ship run
- **WHEN** an auto ship run calls AskUserQuestion
- **THEN** the hook denies the call
- **AND** no record file exists

#### Scenario: Normal run
- **WHEN** a normal session calls AskUserQuestion
- **THEN** one question record exists for its tool use id

### Requirement: Permission wait write
The `record-permission-wait` hook SHALL write a `permission` record with header `Permission` for a `Notification` event of type `permission_prompt`. It SHALL ignore other notification types.

#### Scenario: Permission prompt
- **WHEN** a `Notification` event has `notification_type` `permission_prompt` and message `Claude needs your permission to use Bash`
- **THEN** `.sdlc-v2/evidence/attention/s1-permission.json` exists with `text` `Claude needs your permission to use Bash`

#### Scenario: Other notification
- **WHEN** a `Notification` event has type `idle_prompt`
- **THEN** no record is written

### Requirement: Wait close
The `close-permission-wait` hook SHALL delete the session permission record on `PostToolUse` and `PostToolUseFailure`. The `close-session-waits` hook SHALL delete every record of the session on `SessionEnd`. A delete of a missing file SHALL not be an error, and a delete error SHALL not change the hook output.

#### Scenario: Tool call ends
- **WHEN** a session with a permission record finishes a tool call
- **THEN** the permission record does not exist

#### Scenario: Session ends
- **WHEN** session `s1` ends with 2 records
- **THEN** no record of `s1` exists

### Requirement: Fast close hook
The `close-permission-wait` hook SHALL run as an async hook on every tool call. With no record, it SHALL do no write and SHALL return after one directory lookup.

#### Scenario: No record
- **WHEN** a tool call ends and the session has no permission record
- **THEN** the hook writes nothing
- **AND** the hook output is empty with exit code 0
