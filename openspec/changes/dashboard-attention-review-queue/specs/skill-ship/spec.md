# Spec Delta

## MODIFIED Requirements

### Requirement: Sub-skill dispatch contract
The skill SHALL dispatch each sub-skill as the table says, always with a fixed `model`, never with an `isolation` value, and with a prompt whose first line is `/sdlc:<skill> <args>` followed only by the named payload.

| Step | Dispatch | Model | Args | Tracking |
|---|---|---|---|---|
| `execute` | Agent → `execute` | sonnet | `{flags.executeDispatchArgs} <plan-path>`; never `--branch`, never `--auto` | `begin-step` / `complete-step` |
| `commit` | Agent → `commit` | haiku | `--auto` (= `flags.auto`) | `begin-step` / `complete-step` |
| `review` | Agent → `review` | sonnet | none, or `--base <branch>`; never `--committed` | `begin-step` / `complete-step` |
| `received-review` | Agent → `received-review` | opus | `[--pr <N>] [--auto] [--no-harden]`; payload: collected findings | `begin-step` / `complete-step`, or `fail` |
| `commit-fixes` | Agent → `commit` | haiku | `--auto` | `decide` only |
| `harden` | `Skill` tool → `harden` (never Agent) | — | see the harden requirement | `begin-step` / `complete-step` or `skip` |
| `pr` | Agent → `pr` | sonnet | `[--draft] [--base <branch>] --skip-approval [--auto]` | `begin-step` / `complete-step` |
| `verify-pipeline` fix | Agent → `verify-pipeline` | sonnet | `--pr <N> --logs "<ext.checks_raw>" --auto` | inside `verify-pipeline` |
| `await-remote-review` fix | Agent → `received-review` | opus | `--pr <N> [--auto]` | inside `await-remote-review` |

- `--auto` comes from `flags.auto`, except `commit-fixes`, the harden commit, and the `verify-pipeline` fix dispatch, which always pass `--auto`.
- `--no-harden` is added when `harden` is in `flags.steps`.
- `--skip-approval` is always passed to `pr`; it does not imply `--auto`.
- `--draft` is passed to `pr` when `flags.draft` is `true`. The `pr` skill forwards it to `pr_apply` as `draft: true`, so a newly created PR is a draft; an already-open PR is updated, not converted.
- The skill never calls a sub-skill's own MCP tools, for example `pr_apply`.

#### Scenario: pr dispatch under auto
- **WHEN** `flags.auto` is `true` and `flags.draft` is `true`
- **THEN** the `pr` Agent prompt starts with `/sdlc:pr --draft --skip-approval --auto`
- **AND** the Agent call has `model: sonnet` and no `isolation`

#### Scenario: received-review with harden configured
- **WHEN** `harden` is in `flags.steps` and findings were collected
- **THEN** the `received-review` prompt includes `--no-harden`

### Requirement: received-review and commit-fixes
The skill SHALL dispatch `received-review` only when at least one finding was collected, SHALL dispatch `commit-fixes` only when `received-review` made changes, SHALL record `received-review` with `begin-step` and then `complete-step` or `fail`, and SHALL record `commit-fixes` with `decide` only.

- `ship_prepare` seeds a `received-review` entry right after `review`, with a `condition` key. `commit-fixes` has no `steps[]` entry; the skill never calls `begin-step`, `complete-step`, `start`, `complete`, `skip`, or `fail` for it.
- They are not configurable: `ship_prepare` rejects either name set to `true` in the `[ship.steps]` table, in the `[ship.quick]` table, or in `--steps` with an error.
- The skill calls `begin-step received-review` after the count cross-check. When parsed headings are fewer than `M`, it calls `begin-step` only after the user answers.
- With `K` collected findings, the skill dispatches `received-review`, runs `commit-fixes` when files changed, then calls `complete-step` with result `K finding(s) sent to received-review`.
- With no collected finding, the skill calls `complete-step` with a result that starts with `0 fixes —` and goes to rebase.
- A dispatch error, a user stop, a dispatch with no result, or a failed `commit-fixes` gives `fail` with `detail.error` `received-review: <reason>`.
- On resume, an `in_progress` or `failed` entry runs step 8 again. A `completed` entry goes to rebase, then to the next configured main-loop step. `next` skips a `pending` entry.
- The `received-review` payload is `Review findings to address (from /review, <K> of <M>):` followed by each collected finding's heading, `**File:**` line, and body verbatim.
- `received-review` pauses for the user unless `flags.auto`.
- `commit-fixes` is a separate commit, never squashed into the feature commit.

#### Scenario: No collected findings
- **WHEN** every finding is below the threshold
- **THEN** neither `received-review` nor `commit-fixes` is dispatched
- **AND** the skill calls `begin-step` and then `complete-step` for `received-review` with a result that starts with `0 fixes —`

#### Scenario: Findings collected before a PR exists
- **WHEN** findings are collected and no PR exists yet
- **THEN** the `received-review` prompt has no `--pr` and carries every collected finding

#### Scenario: Dispatch with no result
- **WHEN** the `received-review` dispatch returns no result
- **THEN** the skill calls `fail` for `received-review` with `detail.error` that starts with `received-review:`

#### Scenario: Resume after completion
- **WHEN** the run resumes and the `received-review` entry is `completed`
- **THEN** the skill makes no `received-review` call and runs rebase
