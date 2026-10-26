# Spec Delta

## MODIFIED Requirements

### Requirement: Plan run session ID
After each `plan_prepare` tool call with a non-empty session ID, the `record-mcp-invocation` hook SHALL write that ID as `sessionId` of the plan run that the `plan_prepare` result names in its `runId`, not of the newest active plan run of the branch. The hook SHALL make no write when the stored ID is the same, when the result names no run, or when the named run has no state file or is not active. A state read or write error SHALL leave the hook silent with exit code 0.

- The hook finds the run ID in the Markdown form `runId: plan-...` and in the JSON form `"runId":"plan-..."` of the result. A branch slug with upper-case letters matches.
- Active means `planIntegrity.skillInvoked` set and `planIntegrity.done` absent.
- The hook replaces a different stored `sessionId` and keeps every other field of the run.

#### Scenario: First call
- **WHEN** `plan_prepare` runs in session `s-1`
- **AND** its result names an active run with no `sessionId`
- **THEN** that plan state has `sessionId` `s-1`

#### Scenario: Resume in a new session
- **WHEN** `plan_prepare` runs in session `s-2`
- **AND** its result names a run with `sessionId` `s-1`
- **THEN** that plan state has `sessionId` `s-2`

#### Scenario: No active plan run
- **WHEN** `plan_prepare` runs and its result names a run that has `planIntegrity.done`
- **THEN** no state file changes

#### Scenario: Write error
- **WHEN** the state folder is read-only
- **AND** the `plan_prepare` result names an active run with another `sessionId`
- **THEN** the hook exits 0 and writes one stderr line that starts with `[sdlc/hook record-mcp-invocation] plan session stamp:`

#### Scenario: Only the named run
- **WHEN** branch `main` has active runs A and a newer B
- **AND** `plan_prepare` runs in session `s-1` and its result names A
- **THEN** A has `sessionId` `s-1`
- **AND** the state file of B is byte-identical

#### Scenario: No runId in the result
- **WHEN** the `plan_prepare` result holds no `runId`
- **THEN** no state file changes

#### Scenario: Named run has no file
- **WHEN** the `plan_prepare` result names a run with no state file
- **THEN** no state file changes
- **AND** the hook prints nothing on stderr

## ADDED Requirements

### Requirement: Post-compact plan line picks the run of the session
After a compaction, the session-start hook SHALL print the `Active plan (post-compact):` line for the newest active plan run of the branch whose `sessionId` is the hook session ID, else for the newest active run with no `sessionId`, and SHALL print no plan line when neither exists.

- Active means `skillInvoked` set, `done` absent, and a state write less than 24 hours ago.
- With an empty session ID, the hook uses the newest active run.
- A failure to list the runs prints no plan line.

#### Scenario: Two active runs
- **WHEN** branch `main` has active run A with `sessionId` `s-1` and a newer active run B with `sessionId` `s-2`
- **AND** session `s-1` compacts
- **THEN** the `Active plan (post-compact):` line names run A
- **AND** the `Resume with:` line holds `runId:"<runId of A>"` twice

#### Scenario: Resume line text
- **WHEN** the hook prints the plan line for run `plan-main-20261026T084445Z`
- **THEN** the output holds `  Resume with: plan_prepare({resume:true, resolveTemplate:true, skipConfigCheck:true, runId:"plan-main-20261026T084445Z"}) then plan_support({action:"evidence_digest", runId:"plan-main-20261026T084445Z"})`

#### Scenario: No run of this session
- **WHEN** every active run of the branch has another `sessionId`
- **AND** the session compacts
- **THEN** the hook prints no `Active plan (post-compact):` line

#### Scenario: Stale run
- **WHEN** the only run of this session was last written more than 24 hours ago
- **THEN** the hook prints no `Active plan (post-compact):` line

#### Scenario: Runs path is a file
- **WHEN** `<main-worktree>/.sdlc-v2/runs` is a regular file
- **AND** the session compacts
- **THEN** the hook prints no `Active plan (post-compact):` line

### Requirement: Stop hook checks the plan run of the session
The Stop hook SHALL check the plan integrity gates on the newest plan run of the branch whose `sessionId` is the hook session ID, else on the newest plan run with no `sessionId`, and SHALL use the newest plan run of the branch when neither exists.

- The run may be done; the check does not require an active run.
- With an empty session ID, the hook uses the newest plan run of the branch.
- A failure to list the runs gives no plan run, and the hook uses its transcript check.

#### Scenario: Own run wins over a newer run
- **WHEN** branch `main` has run A with `sessionId` `s-1` and a newer run B with `sessionId` `s-2`
- **AND** the Stop hook runs in session `s-1`
- **THEN** the hook checks the gates of run A

#### Scenario: No run of this session
- **WHEN** every run of the branch has another `sessionId`
- **THEN** the hook checks the gates of the newest plan run of the branch

#### Scenario: Runs path is a file
- **WHEN** `<main-worktree>/.sdlc-v2/runs` is a regular file
- **THEN** the hook finds no plan run
- **AND** uses its transcript check
