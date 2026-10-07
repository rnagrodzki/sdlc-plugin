# Spec Delta

## MODIFIED Requirements

### Requirement: Pipeline validation
The tool SHALL collect validation problems into `errors` and `warnings`, and SHALL NOT create a state file when `errors` is non-empty.

| Condition | Goes to | Text (short) |
|---|---|---|
| `cleanup` in the step list | error | `"cleanup" is a reserved terminal step appended automatically by the pipeline. ...` |
| `received-review` or `commit-fixes` in the step list, from any tier | error | `"<s>" in --steps is a conditional step the pipeline dispatches itself when review findings need fixing — remove it. Valid values: ...` (`steps[]` in place of `--steps` when not from input) |
| Unknown step name, `sources.steps` is `cli` | error | `Unrecognized step "<s>" in --steps. Valid values: execute, commit, review, harden, verify-openspec, archive-openspec, pr, verify-pipeline, await-remote-review, learnings-commit` |
| Unknown step name from any other tier | warning | same text, with `steps[]` in place of `--steps` |
| `quality` not `full`/`balanced`/`minimal` | error | `Invalid --quality "<q>". Valid values: full, balanced, minimal` |
| `reviewThreshold` not exactly `critical`/`high`/`medium`/`low`/`info` | error | `invalid reviewThreshold "<t>": use one of critical, high, medium, low, info in [ship] of .sdlc-v2/local.toml (or ~/.sdlc/local.toml)` |
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
