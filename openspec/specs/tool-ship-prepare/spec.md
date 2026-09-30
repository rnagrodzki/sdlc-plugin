# tool-ship-prepare Specification

## Purpose
MCP tool `ship_prepare` merges ship CLI flags with ship config, validates the resolved pipeline, and initializes the ship state file for a new run; with `gc:true` it prunes stale state files instead. The `ship` skill calls it at most once per run. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input fields
The tool SHALL accept these input fields; every field is optional.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `skipConfigCheck` | bool | no | JSON bool | Skip the config-version gate. |
| `hasPlan` | bool | no | JSON bool | A plan exists for this run. |
| `planFile` | string | no | plain text path, e.g. `"docs/plan.md"` | Plan for the `execute` step. |
| `auto` | bool | no | JSON bool | Run unattended. Overrides config `ship.auto` only when `true`. |
| `steps` | string[] | no | JSON array, e.g. `["commit","pr"]` | Explicit ordered step list. |
| `quick` | bool | no | JSON bool | Use config `ship.quick` as the step list. |
| `quality` | string | no | enum `full` \| `balanced` \| `minimal` | Quality level forwarded to `execute`. |
| `bump` | string | no | plain text, e.g. `"minor"` | Release bump level. |
| `draft` | bool | no | JSON bool | Open the PR as a draft. Overrides config only when `true`. |
| `rebase` | string | no | plain text, e.g. `"skip"` | Rebase strategy. |
| `dryRun` | bool | no | JSON bool | Recorded in `flags.dryRun`. State is still initialized. |
| `resume` | bool | no | JSON bool | Recorded in `flags.resume`. |
| `openspecChange` | string | no | plain text, e.g. `"add-x"` | Recorded in `flags.openspecChange` (`null` when empty). |
| `hookActivePipeline` | bool | no | JSON bool | Recorded in `flags.hookActivePipeline`. |
| `planModeBlocked` | bool | no | JSON bool | Recorded in `flags.planModeBlocked`. |
| `gc` | bool | no | JSON bool | Run the GC branch instead of a pipeline init. |
| `ttlDays` | int | no | JSON integer, e.g. `14`; `0` is legal | GC time-to-live in days. |
| `sessionId` | string | no | plain text | Stamped into the state's `sessionId`. |

#### Scenario: Pass-through flags recorded
- **WHEN** the call passes `hasPlan:true`, `dryRun:true`, and `openspecChange:""`
- **THEN** `flags.hasPlan` and `flags.dryRun` are `true`
- **AND** `flags.openspecChange` is `null`

### Requirement: Config-version gate
Unless `skipConfigCheck` is `true`, the tool SHALL check the project config version before any other work and SHALL return a successful call with one `errors` entry prefixed `config-version:` when the check fails.

- The error path writes no state file and returns empty `flags`, `sources`, `warnings`, and `prunedOrphans`.
- A current config is not touched: no backup file, no `migration` field.
- The gate also runs before the `gc` branch.

#### Scenario: Missing config
- **WHEN** the project has no `.sdlc-v2/config.toml`
- **THEN** `errors` holds exactly one entry that mentions `/setup`
- **AND** `stateFile` is empty

#### Scenario: Legacy JSON config
- **WHEN** the project has only `.sdlc-v2/config.json`
- **THEN** `errors[0]` mentions `/setup`
- **AND** `migration` is absent
- **AND** no file is created under `.sdlc-v2/runs/`

#### Scenario: Current config
- **WHEN** `.sdlc-v2/config.toml` is already current
- **THEN** `errors` is empty
- **AND** no `.sdlc-v2/config.toml.bak` is written

#### Scenario: GC blocked by bad config
- **WHEN** the call passes `gc:true` and the config is missing
- **THEN** `errors` holds the `config-version:` entry and no GC runs

### Requirement: Flag merge precedence
The tool SHALL resolve each flag from input, then config `[ship]`, then the built-in default, and SHALL record the winning tier per key in `sources`.

| Flag key | Precedence | Default |
|---|---|---|
| `steps` | non-empty `steps` input (`cli`) > `quick:true` → config `quick` array (`quick`) > config `steps` array, even empty (`config`) > default | `["execute","commit","review","archive-openspec","pr","learnings-commit"]` |
| `auto`, `draft` | input `true` (`cli`) > config bool > default | `false` |
| `quality` | input only; key omitted when not given | none |
| `bump` | see the bump requirement | `"patch"` |
| `reviewThreshold` | config string > default | `"info"` |
| `rebase` | input string > config (`true`→`"auto"`, `false`→`"skip"`, string verbatim, any other type passed through) > default | `"auto"` |
| `verifyPipelineTimeout` | config number > default | `1200` |
| `verifyPipelineInterval` | config number > default | `60` |
| `verifyPipelineMaxIterations` | config number > default | `3` |
| `awaitRemoteReviewTimeout` | config number > default | `600` |
| `awaitRemoteReviewInterval` | config number > default | `60` |
| `awaitRemoteReviewers` | non-empty config array > default | `["copilot"]` |
| `executeWaveTimeout` | config number > default | `1800` |
| `executeWaveInterval` | config number > default | `60` |
| `executeCommitWaves` | config `execute.commitWaves` bool > default | `true` |

- `sources` values: `cli`, `quick`, `config`, `default`, plus the three bump-policy values below.
- A non-boolean `execute.commitWaves` sets `flags.commitWavesInvalidType:true` and keeps the default.

#### Scenario: Steps from config
- **WHEN** config `[ship]` sets `steps = ["commit","pr"]` and the input has no `steps`
- **THEN** `flags.steps` is `["commit","pr"]`
- **AND** `sources.steps` is `"config"`

#### Scenario: Defaults with no config
- **WHEN** no `[ship]` section exists and the input sets no flags
- **THEN** `flags.bump` is `"patch"` and `sources.steps` is `"default"`

#### Scenario: Malformed rebase config passes through
- **WHEN** config `[ship]` sets `rebase` to a number
- **THEN** `flags.rebase` holds that number
- **AND** `sources.rebase` is `"config"`

#### Scenario: reviewThreshold from config
- **WHEN** config `[ship]` sets `reviewThreshold = "low"`
- **THEN** `flags.reviewThreshold` is `"low"` and `sources.reviewThreshold` is `"config"`

### Requirement: Bump resolution with release policy
The tool SHALL resolve `bump` as input > config `ship.bump` > `"patch"`, then apply config `version.preRelease` and `version.preReleasePolicy` in that order.

| Step | Rule | `sources.bump` |
|---|---|---|
| 1 | `bump` input wins; else config `ship.bump`; else `"patch"` | `cli` / `config` / `default` |
| 2 | `version.preRelease` matching `^[a-z][a-z0-9]*$` replaces a non-`cli` bump | `config (version.preRelease)` |
| 3 | `preReleasePolicy: "always-rc"` replaces any non-RC bump with `"rc"`, CLI included | `config (version.preReleasePolicy)`, or `config (version.preReleasePolicy enforced over cli)` when it replaced a CLI value |
| 3 | `preReleasePolicy: "default-rc"` replaces a non-RC bump with `"rc"` only when the bump did not come from input | `config (version.preReleasePolicy)` |

- A bump counts as RC when it is `rc` or any label matching `^[a-z][a-z0-9]*$` other than `major`, `minor`, `patch`.
- `flags.bump` is never empty.

#### Scenario: always-rc overrides CLI bump
- **WHEN** the call passes `bump:"minor"` and config sets `version.preReleasePolicy = "always-rc"`
- **THEN** `flags.bump` is `"rc"`
- **AND** `sources.bump` is `"config (version.preReleasePolicy enforced over cli)"`
- **AND** `warnings` contains `preReleasePolicy "always-rc" overrode explicit CLI --bump "minor" to "rc"`

#### Scenario: default-rc yields to CLI bump
- **WHEN** the call passes `bump:"minor"` and config sets `version.preReleasePolicy = "default-rc"`
- **THEN** `flags.bump` is `"minor"` and `sources.bump` is `"cli"`

#### Scenario: default-rc without CLI bump
- **WHEN** the input has no `bump` and config sets `version.preReleasePolicy = "default-rc"`
- **THEN** `flags.bump` is `"rc"`

#### Scenario: preRelease wins over policy
- **WHEN** config sets `version.preRelease = "rc"` and `preReleasePolicy = "always-rc"`, and the input has no `bump`
- **THEN** `flags.bump` is `"rc"`
- **AND** `sources.bump` is `"config (version.preRelease)"`

#### Scenario: Other policy values do nothing
- **WHEN** config sets `version.preReleasePolicy = "continue-rc"` and nothing else
- **THEN** `flags.bump` is `"patch"` and `sources.bump` is `"default"`

### Requirement: Pipeline validation
The tool SHALL collect validation problems into `errors` and `warnings`, and SHALL NOT create a state file when `errors` is non-empty.

| Condition | Goes to | Text (short) |
|---|---|---|
| `cleanup` in the step list | error | `"cleanup" is a reserved terminal step appended automatically by the pipeline. ...` |
| `received-review` or `commit-fixes` in the step list, from any tier | error | `"<s>" in --steps is a conditional step the pipeline dispatches itself when review findings need fixing — remove it. Valid values: ...` (`steps[]` in place of `--steps` when not from input) |
| Unknown step name, `sources.steps` is `cli` | error | `Unrecognized step "<s>" in --steps. Valid values: execute, commit, review, harden, verify-openspec, archive-openspec, pr, verify-pipeline, await-remote-review, learnings-commit` |
| Unknown step name from any other tier | warning | same text, with `steps[]` in place of `--steps` |
| `quality` not `full`/`balanced`/`minimal` | error | `Invalid --quality "<q>". Valid values: full, balanced, minimal` |
| `reviewThreshold` not exactly `critical`/`high`/`medium`/`low`/`info` | error | `invalid reviewThreshold "<t>": use one of critical, high, medium, low, info in [ship] of .sdlc-v2/local.toml` |
| Resolved step list is empty | error | `All steps are skipped. At least one step must run.` |
| `hasPlan:true`, `execute` in steps, `planFile` empty | error | `ship cannot run the "execute" step without a plan document. Fix: re-run with --plan <path-to-plan.md>. ...` |
| `bump` from input and `pr` not in steps | error | `--bump "<b>" specified but pr step is skipped — resolve by removing --bump or adding "pr" to ship.steps[].` |
| `quick:true` with a non-empty `steps` input | error | `--quick + --steps not allowed: use --quick or --steps, not both` |
| `quick:true` and config `quick` is unset or empty | error | `No quick profile defined. Run \`ship --init-config\` to set one.` |
| `[version]` config read fails for a reason other than not-found | error | `version config: <cause>` |
| `execute.commitWaves` is not a boolean | warning | `execute.commitWaves in ship config is not a boolean — value ignored, defaulting to true. ...` |
| Always | warning | `If review finds critical/high issues, pipeline will pause for fix approval` |
| Current branch equals the git default branch | warning | `You are on the default branch "<b>". Ship pipelines should run on feature branches.` |

#### Scenario: Execute without plan
- **WHEN** the call passes `hasPlan:true`, `steps:["execute","commit"]`, and no `planFile`
- **THEN** `errors` is non-empty
- **AND** `stateFile` is empty

#### Scenario: Invalid reviewThreshold
- **WHEN** config `[ship]` sets `reviewThreshold = "LOW"`
- **THEN** `errors` has an entry naming every value `critical`, `high`, `medium`, `low`, `info`
- **AND** no `ship-*.json` file exists under `.sdlc-v2/runs/`

#### Scenario: Reserved step
- **WHEN** the call passes `steps:["commit","cleanup"]`
- **THEN** `errors` is non-empty

#### Scenario: Conditional step in config
- **WHEN** config `[ship]` sets `steps = ["commit", "received-review"]` and the input has no `steps`
- **THEN** `errors` has one entry naming `"received-review"` as a conditional step and listing every valid step
- **AND** `warnings` has no entry naming `received-review`
- **AND** no `ship-*.json` file exists under `.sdlc-v2/runs/`

#### Scenario: Conditional step in input
- **WHEN** the call passes `steps:["commit","commit-fixes"]`
- **THEN** `errors` has one entry naming `"commit-fixes"` as a conditional step in `--steps`

#### Scenario: Bump without pr step
- **WHEN** the call passes `steps:["commit"]` and `bump:"minor"`
- **THEN** `errors` has an entry containing `pr step is skipped`

#### Scenario: Default-branch warning without pr
- **WHEN** the call runs on `main` with `steps:["commit"]`
- **THEN** `warnings` contains `You are on the default branch "main". Ship pipelines should run on feature branches.`

### Requirement: Default-branch push gate
The tool SHALL return a `DomainError` when the current branch is exactly `main` or `master` and `pr` is in the resolved step list, regardless of config.

- Message: `ship cannot run the "pr" step on default branch "<b>" — pushing to main/master is never auto-approved`.
- Suggestion: switch to a feature branch, or remove `"pr"` from `--steps`/`ship.steps[]`.
- The gate is decided by the branch name only, not by git config, and it wins over any collected `errors`.

#### Scenario: Default steps on main
- **WHEN** the call runs on `main` with the default steps
- **THEN** the tool returns a `DomainError`

#### Scenario: No pr step on main
- **WHEN** the call runs on `main` with `steps:["commit"]`
- **THEN** the call succeeds

#### Scenario: Feature branch
- **WHEN** the call runs on `feature/x` with the default steps
- **THEN** the call succeeds

### Requirement: State initialization
On a clean validation the tool SHALL write a new ship state file at `.sdlc-v2/runs/ship-<branch-slug>-<YYYYMMDDTHHMMSSZ>.json` under the main worktree root, and SHALL delete every other ship state file with the same branch slug.

- Seeded keys: `version:1`, `startedAt`, `branch`, `worktree`, `sessionId`, `flags`, `sources`, `versionCfg`, `binaryVersion`, `steps`, `decisions:[]`, `deferredFindings:[]`.
- `sideEffects` and `healing` are not seeded.
- `steps` has one `{name, status:"pending", kind}` entry per resolved step, in order.
- `kind` is `"tracked"` for `execute`, `commit`, `review`, `pr`; `"inline"` for every other name. `received-review` and `commit-fixes` are never seeded: Pipeline validation rejects them.
- Deleted files are listed in `prunedOrphans`; files of other branch slugs are kept.
- `pipelineDisplay` renders the seeded steps as a table with one description per step.

#### Scenario: Fresh init
- **WHEN** the call succeeds on `feat/my-feature` with `sessionId:"sess-123"`
- **THEN** `stateFile` is inside `.sdlc-v2/runs/`
- **AND** the file's `steps[0]` is `{name:"execute", status:"pending", kind:"tracked"}`
- **AND** the file's `sessionId` is `"sess-123"`

#### Scenario: Same-branch orphan pruned
- **WHEN** an older `ship-<same-slug>-*.json` file exists
- **THEN** that file is deleted and listed in `prunedOrphans`
- **AND** a file for a different slug that starts with the same text is kept

### Requirement: Execute dispatch arguments
The tool SHALL compute `flags.executeDispatchArgs` as the argument string for the `execute` sub-skill.

```text
[--quality <q>] --wave-timeout <executeWaveTimeout> --wave-interval <executeWaveInterval> [--commit-waves <bool>]
```

- `--quality` appears only when `quality` was given.
- `--commit-waves` appears only when `sources.executeCommitWaves` is `"config"`.

#### Scenario: Quality absent
- **WHEN** config sets `executeWaveTimeout = 900` and `executeWaveInterval = 30`, and the input has no `quality`
- **THEN** `flags.executeDispatchArgs` is `"--wave-timeout 900 --wave-interval 30"`

#### Scenario: Quality present
- **WHEN** the same config applies and the input has `quality:"full"`
- **THEN** `flags.executeDispatchArgs` is `"--quality full --wave-timeout 900 --wave-interval 30"`

#### Scenario: commitWaves configured
- **WHEN** config `[ship]` sets `execute.commitWaves = false` and nothing else
- **THEN** `flags.executeDispatchArgs` is `"--wave-timeout 1800 --wave-interval 60 --commit-waves false"`

#### Scenario: commitWaves not configured
- **WHEN** config `[ship]` has no `execute.commitWaves`
- **THEN** `flags.executeDispatchArgs` has no `--commit-waves`

### Requirement: GC branch
When `gc` is `true`, the tool SHALL skip flag merge, validation, and state init, and SHALL prune stale state files and explore tempdirs instead.

- TTL: `ttlDays` input > config `state.gc.ttlDays` (integer ≥ 0) > `7`. `0` means no grace period.
- A branch is live when `git branch --list` shows it locally. If that command fails, or lists no branch at all (unborn HEAD), every branch counts as live, so no file is deleted for branch reasons.
- Every state file for a branch that no longer exists locally is deleted, whatever its age.
- For a live branch, the newest file is always kept; an older file is deleted once its age exceeds the TTL.
- Explore tempdirs (`sdlc-explore-*`) are swept from the system temp dir, or from `SDLC_EXPLORE_TMPDIR_OVERRIDE` when set.
- No state file is written.

| Output field | Meaning |
|---|---|
| `action` | `"gc"` |
| `report.ttlDays` | Resolved TTL. |
| `report.ship` / `execute` / `plan` / `commit` | `{deleted, kept}` path lists for that state-file prefix. |
| `report.exploreTempdirs` | `{deleted, kept}` path lists; `[]` never `null`. |

#### Scenario: ttlDays zero
- **WHEN** the call passes `gc:true, ttlDays:0`
- **THEN** `report.ttlDays` is `0`
- **AND** a 1-second-old state file for a deleted branch is in `report.ship.deleted`

#### Scenario: Config TTL
- **WHEN** the call passes `gc:true` with no `ttlDays` and config sets `state.gc.ttlDays = 1`
- **THEN** `report.ttlDays` is `1`

#### Scenario: Local branch not checked out
- **WHEN** a stale state file belongs to a branch that exists locally but is not checked out
- **THEN** it is in `report.ship.kept`

#### Scenario: Branch list unavailable
- **WHEN** `git branch --list` fails in the active worktree
- **AND** a stale state file exists
- **THEN** the file is not deleted
- **AND** `report.ship.deleted` is empty

#### Scenario: GC sweep fails
- **WHEN** the sweep itself fails
- **THEN** `action` is `"gc"`, `errors` holds `gc failed: <cause>`, and `report` is absent

### Requirement: Output fields
The tool SHALL return these tool-specific fields.

| Field | Meaning |
|---|---|
| `errors` | Validation or gate errors. Non-empty means no state was created. |
| `warnings` | Non-blocking notices. |
| `flags` | Merged flags; the same object is written to the state's `flags`. |
| `sources` | Winning tier per flag key. |
| `versionCfg` | Raw `[version]` config section as read. |
| `binaryVersion` | Build info of the `sdlc` binary. |
| `branch` | Current branch. |
| `worktree` | Active worktree root. |
| `stateFile` | Path of the new state file; empty when none was written. |
| `prunedOrphans` | Same-branch ship state files deleted. |
| `pipelineDisplay` | Rendered step table; only after state init. |
| `migration` | `{changes, backupPath}`; present only when the config gate migrated the config and wrote a backup. |
| `action`, `report` | GC branch only. |
| `next` | See the table below. |

| Outcome | `next` |
|---|---|
| Normal path, errors | `Fix the errors above, then call ship_prepare again.` |
| Normal path, success | `Confirm release level, then call ship_state with action:"begin-step" for the first step in flags.steps.` |
| GC, errors | `GC failed. Fix the errors above and retry ship_prepare with gc:true.` |
| GC, success | `GC complete. No further action needed.` |

#### Scenario: Success next line
- **WHEN** validation passes and state is written
- **THEN** `next` is `Confirm release level, then call ship_state with action:"begin-step" for the first step in flags.steps.`

### Requirement: Infrastructure errors
The tool SHALL return an `InfraError` when it cannot resolve the project root or read or write the state directory.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Not inside a git repository | `InfraError` | `resolve project root: ...` / run from inside a git repository |
| Listing `.sdlc-v2/runs/` fails | `InfraError` | `scan existing ship state files: ...` / check read permission on `.sdlc-v2/runs/` |
| Creating the state file fails | `InfraError` | `init ship state: ...` / check write permission and disk space |
| Writing the state file fails | `InfraError` | `write ship state: ...` / check write permission and disk space |

#### Scenario: Run outside a repository
- **WHEN** the tool is called outside any git repository
- **THEN** it returns an `InfraError` whose message starts with `resolve project root:`
