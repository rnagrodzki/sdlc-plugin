# skill-openspec-save Specification

## Purpose
The `/sdlc:openspec-save` skill saves the OpenSpec change that the plan staged as its own branch, commit, and PR, so reviewers approve the specs before the code ships.

## Requirements

### Requirement: Arguments
The skill SHALL take the plan path as `--plan <path>` or as a positional path, and an optional `--auto`. It SHALL never infer the plan. Two different paths SHALL stop with an error.

#### Scenario: Two paths
- **WHEN** the user passes `--plan a.md` and the positional path `b.md`
- **THEN** the skill stops with an error

### Requirement: Save, commit, and PR flow
The skill SHALL call `openspec_save`, then run the commit skill with `--type docs --scope openspec` only when `stagedFiles` is not empty, then run the pr skill without `--auto`, in the same session. It SHALL report the change, branch, and PR URL, and tell the user to merge the PR before ship.

#### Scenario: Tool error
- **WHEN** `openspec_save` returns an error
- **THEN** the skill prints the error and its Suggestion and stops

#### Scenario: Already committed
- **WHEN** `openspec_save` returns empty `stagedFiles`
- **THEN** the skill does not run the commit skill
- **AND** it runs the pr skill

#### Scenario: Commit declined
- **WHEN** the user declines the commit
- **THEN** the skill stops and reports the branch

#### Scenario: Auto mode
- **WHEN** the user passes `--auto`
- **THEN** the commit skill gets `--auto`
- **AND** the pr skill does not get `--auto`
