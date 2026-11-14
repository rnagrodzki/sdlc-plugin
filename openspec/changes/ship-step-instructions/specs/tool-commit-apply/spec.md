# Spec Delta

## ADDED Requirements

### Requirement: Optional best-effort push
With `push: true`, the tool SHALL push the branch to `origin` after the commit. A push failure SHALL NOT fail the call: `hash` stays set, `pushed` is false, and `warnings` names the cause.

| Push result | `pushed` | `warnings` |
|---|---|---|
| works | true | absent |
| default branch | false | `push skipped: <branch> is the default branch` |
| no origin remote | false | `push skipped: no remote origin` |
| current branch or remote cannot be read | false | `push skipped: cannot read the current branch: <error>` or `push skipped: cannot read remote origin: <error>` |
| fails | false | `push failed (<kind>): <stderr>. The commit is kept and is not on the remote.` |

When the default-branch lookup fails, the push SHALL still run, and the warning SHALL start with `default-branch check skipped: <error>.` A push that works then gives `pushed` true and a warning that ends with `The branch was pushed.`

#### Scenario: Push omitted
- **WHEN** `push` is omitted
- **THEN** no push runs and `pushed` is false

#### Scenario: Auth failure
- **WHEN** the push fails with `Authentication failed`
- **THEN** the call succeeds with the commit hash
- **AND** `warnings` has `push failed (auth): …`
