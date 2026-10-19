# Spec Delta

## MODIFIED Requirements

### Requirement: State initialization
On a clean validation the tool SHALL write a new ship state file at `.sdlc-v2/runs/ship-<branch-slug>-<YYYYMMDDTHHMMSSZ>.json` under the main worktree root, and SHALL delete each other ship state file with the same branch slug that is not kept. It SHALL keep a completed run that is not a dry-run, and a run that is not a dry-run whose non-empty `sessionId` differs from the non-empty `sessionId` of the call.

- Seeded keys: `version:1`, `startedAt`, `branch`, `worktree`, `sessionId`, `flags`, `sources`, `versionCfg`, `binaryVersion`, `steps`, `decisions:[]`, `deferredFindings:[]`.
- `sideEffects` and `healing` are not seeded.
- `steps` has one `{name, status:"pending", kind}` entry per resolved step, in order.
- When `review` is a resolved step, `steps` also has one `{name:"received-review", status:"pending", kind:"tracked", condition}` entry right after `review`.
- `kind` is `"tracked"` for `execute`, `commit`, `review`, `pr` and the seeded `received-review`; `"inline"` for every other name. `commit-fixes` is never seeded. Pipeline validation rejects `received-review` and `commit-fixes` in the step list.
- Deleted files are listed in `prunedOrphans`; files of other branch slugs are kept. `prunedOrphans` lists exactly the files that the write deleted; a failed delete is not listed, and `prunedOrphans` is `[]`, never `null`, when nothing was deleted.
- A kept file stays until GC (`state.gc.ttlDays`) deletes it.
- `pipelineDisplay` renders the seeded steps as a table with one description per step.

#### Scenario: Fresh init
- **WHEN** the call succeeds on `feat/my-feature` with `sessionId:"sess-123"`
- **THEN** `stateFile` is inside `.sdlc-v2/runs/`
- **AND** the file's `steps[0]` is `{name:"execute", status:"pending", kind:"tracked"}`
- **AND** the file's `sessionId` is `"sess-123"`

#### Scenario: Same-branch orphan pruned
- **WHEN** an older in-flight `ship-<same-slug>-*.json` file exists
- **THEN** that file is deleted and listed in `prunedOrphans`
- **AND** a file for a different slug that starts with the same text is kept

#### Scenario: Completed run kept
- **WHEN** an older `ship-<same-slug>-*.json` file has `pipelineStatus` `completed` and no `flags.dryRun`
- **THEN** that file is kept
- **AND** `prunedOrphans` does not list it

#### Scenario: Dry-run run deleted
- **WHEN** an older `ship-<same-slug>-*.json` file has `flags.dryRun` `true` and `pipelineStatus` `completed`
- **THEN** that file is deleted and listed in `prunedOrphans`

#### Scenario: Run of another session kept
- **WHEN** an older in-flight `ship-<same-slug>-*.json` file has `sessionId` `"s1"`
- **AND** the call passes `sessionId` `"s2"`
- **THEN** that file is kept

#### Scenario: Call with no session id
- **WHEN** an older in-flight `ship-<same-slug>-*.json` file has `sessionId` `"s1"`
- **AND** the call passes no `sessionId`
- **THEN** that file is deleted and listed in `prunedOrphans`

#### Scenario: Seeded received-review entry
- **WHEN** the resolved steps are `execute`, `commit`, `review`, `pr`
- **THEN** `steps[3]` is `received-review` with status `pending`, kind `tracked` and a `condition` key
- **AND** `steps[4]` is `pr`

#### Scenario: No review step
- **WHEN** the resolved steps do not include `review`
- **THEN** `steps` has no `received-review` entry
