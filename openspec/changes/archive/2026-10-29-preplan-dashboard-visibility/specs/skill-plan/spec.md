# Spec Delta

## MODIFIED Requirements

### Requirement: Invocation flags
The skill SHALL accept the arguments in its `argument-hint`: `[--auto] [--spec [<change-name>]] [spec-file-path...]`.

| Flag | Effect |
|---|---|
| `--auto` | Suppresses the structured-discovery and approach-check questions, the OpenSpec gate-check question (option **Create OpenSpec change** is taken), and every **harden** offer. Choices made without asking go into `## Key Decisions` and get a `## Deviations & assumptions` row with `asked=no`. |
| `--spec` | Opts into OpenSpec and skips the gate-check question. Without a name: use the branch-matched change, else ask which active change to use, or offer **Create OpenSpec change** when none exists. With two or more topic-file paths, the skill stops. |
| `--spec <change-name>` | Plans directly from `openspec/changes/<change-name>/`; passed to `plan_prepare` as `fromOpenspec` |
| `--from-openspec <change-name>` | Deprecated alias of `--spec <change-name>` for one release; prints `--from-openspec is deprecated — use --spec <change-name>` |
| `[spec-file-path...]` | Requirements file; a path into `openspec/changes/<name>/` selects that change. Two or more paths must all be preplan topic files. A path in double quotes is one path |

- `--auto` never bypasses a Gate A `CRITICAL` verdict.
- `--auto` still asks three questions that have no safe default: which OpenSpec change to use when several match, whether to split into N plans, and what to do after `reviewLoop.maxRounds` (5) review rounds with open blocking issues. When AskUserQuestion is unavailable, the skill stops and reports instead of choosing.

#### Scenario: Auto mode at the OpenSpec gate check
- **WHEN** `--auto` is set and the gate check finds a functional request with no matching change
- **THEN** the skill does not call AskUserQuestion
- **AND** it takes **Create OpenSpec change** and adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Auto mode with several OpenSpec changes
- **WHEN** `--auto` is set and several active OpenSpec changes exist with no branch match
- **THEN** the skill asks which change to use with AskUserQuestion

#### Scenario: Auto mode resolves an approach question
- **WHEN** `--auto` is set and decomposition reveals two viable approaches
- **THEN** the skill does not call AskUserQuestion
- **AND** it picks the most conservative option, using the closest match to existing codebase patterns as the tiebreaker, and adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Deprecated alias
- **WHEN** the skill is invoked with `--from-openspec add-widget`
- **THEN** it behaves as `--spec add-widget`
- **AND** it prints `--from-openspec is deprecated — use --spec <change-name>`

#### Scenario: Spec flag with several topic files
- **WHEN** the skill is invoked with `--spec` and two topic-file paths
- **THEN** the skill stops and prints `--spec cannot be used with several topic files. Run /sdlc:plan --spec <name>, or /sdlc:plan with topic files only.`

## ADDED Requirements

### Requirement: Several preplan topic files
With two or more paths, the skill SHALL read each preplan topic file in argument order, SHALL number the R-items across all files, and SHALL ask one Structured discovery question for each conflict between two files.

#### Scenario: Two topic files
- **WHEN** the user runs `/sdlc:plan ".sdlc-v2/preplan/2026-10-29-a.md" ".sdlc-v2/preplan/2026-10-29-b.md"`
- **THEN** the requirements of file a come before those of file b, numbered R1 to Rn without a restart

#### Scenario: Per-topic entries
- **WHEN** two topic files are read
- **THEN** `## Key Decisions` has one Non-goals bullet and one Smallest cut bullet for each topic, with the topic name first

#### Scenario: Conflict under auto mode
- **WHEN** `--auto` is set and two files have decisions that contradict each other
- **THEN** the skill takes the decision of the earlier file
- **AND** it adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Source header
- **WHEN** two topic files are read and the OpenSpec gate check sets no source
- **THEN** `**Source:**` lists both paths separated by `, `

#### Scenario: Path that is not a topic file
- **WHEN** the second path is `README.md`
- **THEN** the skill stops and prints `README.md is not a preplan topic file. Run /sdlc:plan with topic files only, or with one requirements file.`
