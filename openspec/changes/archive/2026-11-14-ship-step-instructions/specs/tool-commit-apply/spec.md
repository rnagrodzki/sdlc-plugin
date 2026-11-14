# Spec Delta

## ADDED Requirements

### Requirement: Optional best-effort push
With `push: true`, the tool SHALL push the branch to `origin` after the commit. A push failure SHALL NOT fail the call: `hash` stays set, `pushed` is false, and `warnings` names the cause.

#### Scenario: Push omitted
- **WHEN** `push` is omitted
- **THEN** no push runs and `pushed` is false

#### Scenario: Push works
- **WHEN** the push works
- **THEN** `pushed` is true and `warnings` is absent

#### Scenario: Default branch
- **WHEN** the current branch is the default branch
- **THEN** `pushed` is false and `warnings` has `push skipped: <branch> is the default branch`

#### Scenario: No origin remote
- **WHEN** the repository has no `origin` remote
- **THEN** `pushed` is false and `warnings` has `push skipped: no remote origin`

#### Scenario: Branch or remote cannot be read
- **WHEN** the current branch or the remote cannot be read
- **THEN** `pushed` is false
- **AND** `warnings` has `push skipped: cannot read the current branch: <error>` or `push skipped: cannot read remote origin: <error>`

#### Scenario: Auth failure
- **WHEN** the push fails with `Authentication failed`
- **THEN** the call succeeds with the commit hash
- **AND** `pushed` is false and `warnings` has `push failed (auth): <stderr>. The commit is kept and is not on the remote.`

### Requirement: Push after a failed default-branch lookup
When the default-branch lookup fails, the push SHALL still run, and any warning SHALL start with `default-branch check skipped: <error>.`

#### Scenario: Lookup fails and the push works
- **WHEN** the default-branch lookup fails and the push works
- **THEN** `pushed` is true
- **AND** `warnings` starts with `default-branch check skipped: <error>.` and ends with `The branch was pushed.`
