# tool-ship-verify-side-effect Specification

## Purpose
MCP tool `ship_verify_side_effect` checks that a ship pipeline step's side effect (a PR or a commit sha) really landed, and records a confirmed one in the ship state's `sideEffects` journal so a resumed run can skip that step. The ship skill calls it after its `commit` and `pr` steps succeed. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input fields
The tool SHALL accept a step name and an optional expected value.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `step` | string | yes | plain text, e.g. `"commit"` | Pipeline step whose side effect is checked. |
| `expected` | string | no | plain text, e.g. a 40-char sha | Value the side effect must match. Used by the `sha` kind only. |

#### Scenario: Expected value is echoed
- **WHEN** the call passes `step:"commit"` and `expected:"deadbeef..."`
- **THEN** the output field `expected` holds `"deadbeef..."`

#### Scenario: Expected value omitted
- **WHEN** the call omits `expected`
- **THEN** the output field `expected` renders as `(none)`

### Requirement: Step to side-effect kind mapping
The tool SHALL map `pr` to kind `pr` and `commit` to kind `sha`, and SHALL report every other step as landed with no side effect.

| `step` | `sideEffect` kind | What proves it landed |
|---|---|---|
| `pr` | `pr` | `gh pr view` finds an open PR (state `OPEN`) for the current branch. |
| `commit` | `sha` | `git rev-parse HEAD` equals the comparison sha. |
| any other name (e.g. `review`, `version`) | none | Nothing to check. |

#### Scenario: Step with no side effect
- **WHEN** the call passes `step:"review"`
- **THEN** `landed` is `true`
- **AND** `reason` is `"no-side-effect"`
- **AND** `sideEffect` is empty
- **AND** `next` is `"No side effect to verify. Proceed to the next pipeline step."`

#### Scenario: Removed version step
- **WHEN** the call passes `step:"version"` and `expected:"v9.9.9"`
- **THEN** `landed` is `true` with `reason:"no-side-effect"`

### Requirement: PR side effect
For kind `pr`, the tool SHALL report `landed:true` only when `gh pr view` finds a PR in state `OPEN` for the current branch, and SHALL use `#<number>` as the journal ref.

- With no open PR, `gh pr view` returns the branch's newest closed or merged PR. That PR does not count as landed.

#### Scenario: PR exists
- **WHEN** the call passes `step:"pr"` and the branch has open PR 42
- **THEN** `landed` is `true`
- **AND** the ship state's `sideEffects.pr` is `{kind:"pr", ref:"#42", verifiedAt:<RFC3339 UTC>}`

#### Scenario: No PR
- **WHEN** the call passes `step:"pr"` and `gh pr view` finds no PR for the branch
- **THEN** `landed` is `false`
- **AND** `next` is `"Side effect not yet landed. Retry or investigate."`

#### Scenario: Only a closed or merged PR
- **WHEN** the call passes `step:"pr"` and the branch's only PR is `CLOSED` or `MERGED`
- **THEN** `landed` is `false`
- **AND** no `sideEffects.pr` entry is written

### Requirement: Commit sha side effect
For kind `sha`, the tool SHALL compare `HEAD` against `expected` when given, else against the sha already in the journal, else report not landed.

| `expected` | Journal entry for step | `landed` |
|---|---|---|
| given | any | `HEAD == expected` |
| omitted | present | `HEAD ==` journaled `ref` |
| omitted | absent | `false` |

#### Scenario: Expected sha matches HEAD
- **WHEN** the call passes `step:"commit"` and `expected` equal to `HEAD`
- **THEN** `landed` is `true`
- **AND** `sideEffects.commit.ref` is the `HEAD` sha

#### Scenario: Expected sha differs from HEAD
- **WHEN** the call passes `step:"commit"` and an `expected` that is not `HEAD`
- **THEN** `landed` is `false`

#### Scenario: No baseline
- **WHEN** the call passes `step:"commit"`, omits `expected`, and no journal entry exists
- **THEN** `landed` is `false`
- **AND** no `sideEffects.commit` entry is written

#### Scenario: Resume confirms journaled sha
- **WHEN** a journal entry exists for `commit` and `HEAD` still equals its `ref`
- **THEN** a call without `expected` returns `landed:true`

#### Scenario: HEAD moved after journaling
- **WHEN** a journal entry exists for `commit` and `HEAD` has moved past its `ref`
- **THEN** a call without `expected` returns `landed:false`
- **AND** the journaled `ref` is unchanged

### Requirement: Journal write only on confirmed side effect
The tool SHALL write `sideEffects.<step>` in the current branch's ship state file under `.sdlc-v2/runs/` only when `landed` is `true` and a ship state file exists for the branch.

- The entry shape is `{kind, ref, verifiedAt}`; `kind` is `pr` or `sha`.
- No ship state file for the branch, or an unresolvable branch, is not an error: the check still runs and nothing is written.
- `ship_state` `begin-step` reads this entry to set `alreadyDone`.

#### Scenario: No ship state for the branch
- **WHEN** the call passes `step:"pr"`, the PR exists, and the branch has no ship state file
- **THEN** `landed` is `true`
- **AND** no file is written

#### Scenario: Not landed leaves journal untouched
- **WHEN** the check reports `landed:false`
- **THEN** the ship state file is not written

### Requirement: Output fields
The tool SHALL return these tool-specific fields.

| Field | Meaning |
|---|---|
| `step` | The `step` input, echoed. |
| `sideEffect` | Kind checked: `pr` or `sha`. Empty for a step with no side effect. |
| `landed` | `true` when the side effect is confirmed. |
| `expected` | The `expected` input, echoed; `(none)` when omitted. |
| `reason` | `"no-side-effect"` for a step with no side effect; otherwise empty. |
| `next` | `"Side effect verified. Proceed to the next pipeline step."` when landed; `"Side effect not yet landed. Retry or investigate."` when not. |

#### Scenario: Landed next line
- **WHEN** a `pr` or `sha` check reports `landed:true`
- **THEN** `next` is `"Side effect verified. Proceed to the next pipeline step."`

### Requirement: Errors
The tool SHALL fail with an `InfraError` when it cannot resolve the project root, read `HEAD`, or write the state file.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Not inside a git repository | `InfraError` | `resolve project root: ...` / run from inside a git repository |
| `git rev-parse HEAD` fails (kind `sha`) | `InfraError` | `git rev-parse HEAD: ...` / inspect the worktree with `git status` |
| State file write fails after a landed check | `InfraError` | `write ship state: ...` / check write permission under `.sdlc-v2/runs/` |

- The tool runs no config-version check.

#### Scenario: HEAD cannot be read
- **WHEN** the call passes `step:"commit"` and `git rev-parse HEAD` fails
- **THEN** the tool returns an `InfraError` whose message starts with `git rev-parse HEAD:`
