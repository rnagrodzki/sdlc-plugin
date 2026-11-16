# Spec Delta

## ADDED Requirements

### Requirement: Archive commit in archive-openspec
After `openspec archive` exits 0, the `archive-openspec` step SHALL stage only `openspec/changes/<change>`, `openspec/changes/archive/*-<change>` and `openspec/specs/` with `git add -A`, commit them through the commit skill, and complete the step with the result `archived, commit <sha>`.

#### Scenario: Normal archive
- **WHEN** `openspec archive` exits 0 and the commit Agent returns a `- sha:` bullet
- **THEN** the step completes with the result `archived, commit <sha>`

#### Scenario: Commit fails
- **WHEN** the commit Agent returns an error or no sha
- **THEN** the step fails with `archive commit failed — <error>`

#### Scenario: Resume with an uncommitted archive
- **WHEN** a resume finds the change archived and the archive paths dirty
- **THEN** the step stages and commits them

#### Scenario: Draft of another change
- **WHEN** `openspec/changes/other-change/` has uncommitted files
- **THEN** the archive commit does not include them

### Requirement: Wait between poll probes
Between two `poll_await` probes, the ship skill SHALL call `ship_state` `wait` with `min(interval_seconds + 30, 3600)` seconds, then run the sleep in the background and end the turn. It SHALL NOT use a foreground `sleep` or `gh pr checks --watch`.

#### Scenario: Pending poll
- **WHEN** `poll_await` returns `pending` with `interval_seconds` 60
- **THEN** the skill calls `ship_state({action:"wait", step:"verify-pipeline", detail:{seconds:90}})`
- **AND** starts `sleep 60` in the background

#### Scenario: Background worker
- **WHEN** a step ends its turn while a background Agent runs
- **THEN** the skill first calls `ship_state` `wait` with `detail.seconds` 900

### Requirement: Approved PR draft dispatch
When the user approved a PR title and body in the session, the ship skill SHALL pass them to the pr Agent in an `Approved PR draft (publish unchanged):` block.

#### Scenario: Approved draft
- **WHEN** the user approved a title and body before the pr step
- **THEN** the pr dispatch prompt holds the block with that title and body

#### Scenario: New session
- **WHEN** the pr step runs on resume in a new session
- **THEN** the pr dispatch prompt has no block

### Requirement: Short report print
At the report step, the ship skill SHALL print the one-line `display` and the `path` of the report result, and SHALL print deferred follow-ups by `title`.

#### Scenario: Written report
- **WHEN** `report` returns `written: true`
- **THEN** the skill prints the summary line, then the report path
