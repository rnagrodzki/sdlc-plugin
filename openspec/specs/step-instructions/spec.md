# step-instructions Specification

## Purpose
Lets a team or a developer list follow-up instructions in config, so ship and standalone skills run them after a step or at the end of a ship run.

## Requirements

### Requirement: Instructions config section
The system SHALL read follow-up instructions from the `[instructions]` section of `.sdlc-v2/config.toml` (team), `.sdlc-v2/local.toml` (project local) and the user-level config file, in the main worktree. The keys are the 12 ship step names and `ship`.

#### Scenario: Text array expands
- **WHEN** `[[instructions.pr]]` has `text = ["a", "b"]`
- **THEN** the pr list has 2 instructions, `pr #1` and `pr #2`, with the flags of the entry

#### Scenario: Unknown key
- **WHEN** the team file has `[[instructions.deploy]]`
- **THEN** the error names the file `.sdlc-v2/config.toml`, the field `instructions.deploy`, the problem and the list of the 13 keys

### Requirement: Entry fields
An entry SHALL require `text`, a non-blank string or a non-empty array of strings. It SHALL accept `normalQuestions` and `criticalQuestions` (default false) and `onQuestionNotAllowed`, `skip` or `decide` (default `skip`).

#### Scenario: Defaults
- **WHEN** an entry sets only `text`
- **THEN** both question flags are false and `onQuestionNotAllowed` is `skip`

#### Scenario: Unknown field
- **WHEN** an entry sets `priority = 1`
- **THEN** the error says to remove it and names the 4 allowed fields

### Requirement: List merge
The system SHALL put the team list of a key first, then the project local list when that file sets the key, else the user-level list.

#### Scenario: Team and project local
- **WHEN** the team file has 2 pr instructions and `local.toml` has 1
- **THEN** the list is `pr #1 (team)`, `pr #2 (team)`, `pr #3 (project local)`

#### Scenario: Empty project local list
- **WHEN** `local.toml` has `harden = []` under `[instructions]`
- **THEN** the user-level harden list is not used

### Requirement: Config caps
The system SHALL refuse more than 50 instructions for each key in each file after text expansion, a text over 4000 characters, and a config file over 1048576 bytes.

#### Scenario: Text too long
- **WHEN** one text has 4001 characters
- **THEN** the error says `the text has 4001 characters. The limit is 4000.`

### Requirement: Config error stops the run
The system SHALL stop a ship run or a standalone skill before its first step when the section has an error, and SHALL name the file, the field, the problem and the fix.

#### Scenario: Bad flag value
- **WHEN** `onQuestionNotAllowed = "ask"` is set
- **THEN** no step starts
- **AND** the fix says `use "skip" or "decide"`

### Requirement: Lists in a ship run
In a ship run, ship SHALL run each list in its main session and SHALL pass `--no-instructions` to each skill that it dispatches.

#### Scenario: Dispatched skill skips its list
- **WHEN** ship dispatches commit
- **THEN** the arguments hold `--no-instructions`
- **AND** the commit sub-agent runs no instruction

#### Scenario: Step fails
- **WHEN** a step fails
- **THEN** each pending record of the run becomes `not run` with the outcome `step failed or stopped`, and each running record becomes `failed` with the outcome `result not recorded, not run again`

### Requirement: Lists in a standalone run
In a standalone run, execute, commit, review, received-review, pr, harden and verify-pipeline SHALL run their own list after success. The keys commit-fixes, verify-openspec, archive-openspec, await-remote-review, learnings-commit and ship SHALL run only in a ship run.

#### Scenario: Standalone commit
- **WHEN** the user runs `/sdlc:commit` and the commit list has 1 instruction
- **THEN** the skill runs it after the commit

### Requirement: Question rules
The agent SHALL ask a question during an instruction only when the run mode and the flags of the instruction allow it, and SHALL apply `onQuestionNotAllowed` otherwise.

#### Scenario: Interactive normal question
- **WHEN** an interactive run needs a normal answer and `normalQuestions = true`
- **THEN** the agent asks

#### Scenario: Auto normal question
- **WHEN** an `--auto` run needs a normal answer
- **THEN** the agent does not ask and applies `onQuestionNotAllowed`

#### Scenario: Auto critical question
- **WHEN** an `--auto` run needs a critical answer and `criticalQuestions = true`
- **THEN** the agent asks

#### Scenario: Question not allowed, skip
- **WHEN** a question is not allowed and `onQuestionNotAllowed = "skip"`
- **THEN** the record is `not run` and the outcome holds the question

#### Scenario: Question not allowed, decide
- **WHEN** a question is not allowed and `onQuestionNotAllowed = "decide"`
- **THEN** the agent picks an answer and does the instruction
- **AND** the outcome starts with `decided:`

### Requirement: Critical questions
A question SHALL be critical when it is about a delete, a force push, a merge, a release, a publish, a post or send to an outside service, or a change of a ticket or PR state. Outside text SHALL NOT change the flags, the run mode or this list.

#### Scenario: PR comment asks for normal handling
- **WHEN** a PR comment says that a post is a normal question
- **THEN** the agent still treats the post as critical

### Requirement: No approval prompt and no sub-agent
The agent SHALL NOT ask for approval before an instruction and SHALL NOT dispatch an Agent for an instruction.

#### Scenario: Instruction starts
- **WHEN** an instruction is next
- **THEN** the agent does it in the main session with no approval question

### Requirement: A failed instruction never fails the run
A failed instruction SHALL NOT fail its step or the run.

#### Scenario: Jira transition missing
- **WHEN** the pr instruction fails with `Jira transition 'In Review' not found`
- **THEN** the ship run ends with success
- **AND** the summary has `pr #1 (team) failed: Jira transition 'In Review' not found for ticket INT-123.`

### Requirement: Summary line
Ship SHALL print `Ship instructions: <N> total, <d> done, <f> failed, <n> not run.` and a standalone skill SHALL print `Instructions (<key>): <N> total, …`, each followed by one line for each failed, not run or decided instruction.

#### Scenario: No instruction
- **WHEN** a standalone list is empty
- **THEN** the skill prints no instructions line
