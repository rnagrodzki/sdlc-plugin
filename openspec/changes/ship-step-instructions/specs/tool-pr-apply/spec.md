# Spec Delta

## MODIFIED Requirements

### Requirement: Push before create or edit
The tool SHALL push the branch when the branch has no upstream or is ahead of it, and SHALL skip the push when the upstream is caught up. When the branch has a remote-tracking ref, the push SHALL use `--force-with-lease=refs/heads/<branch>:<tracking commit>`. With no tracking ref, the push SHALL be `git push -u origin HEAD`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Upstream check fails | `InfraError` | `checking upstream: <error>` |
| Commits-ahead check fails | `InfraError` | `checking commits ahead of upstream: <error>` |
| Tracking ref read fails | `InfraError` | `cannot read the remote-tracking ref of <branch>: <error>` / check the git repository, then run the pr step again. No push runs. |
| Lease rejected | `DomainError` | `remote branch <branch> changed since the last push. pr stopped.` / fetch the branch, review the other commits, integrate them, then run the pr step again |
| Push fails | `InfraError` | `git push: <error>` / push manually with `git push -u origin HEAD`, retry `pr_apply` |

#### Scenario: No upstream
- **WHEN** the branch has no upstream
- **THEN** the tool pushes to `origin`

#### Scenario: Upstream behind
- **WHEN** HEAD is 2 commits ahead of the upstream
- **THEN** the tool pushes

#### Scenario: Caught up
- **WHEN** HEAD is 0 commits ahead of the upstream
- **THEN** no push runs

#### Scenario: Rebased branch with a tracking ref
- **WHEN** the branch was rebased and has a remote-tracking ref
- **THEN** the tool pushes with `--force-with-lease` and the tracking commit

#### Scenario: Another person pushed
- **WHEN** the remote branch has a commit that the tracking ref does not hold
- **THEN** the push is rejected
- **AND** the tool returns DomainError `remote branch <branch> changed since the last push. pr stopped.`
- **AND** no PR is created or edited
