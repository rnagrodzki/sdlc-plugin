# Spec Delta

## MODIFIED Requirements

### Requirement: Invocation flags
The skill SHALL accept the arguments in its `argument-hint`: `[--auto] [--spec [<change-name>]] [spec-file-path]`.

| Flag | Effect |
|---|---|
| `--auto` | Suppresses the structured-discovery and approach-check questions, the OpenSpec gate-check question (option **Create OpenSpec change** is taken), and every **harden** offer. Choices made without asking go into `## Key Decisions` and get a `## Deviations & assumptions` row with `asked=no`. |
| `--spec` | Opts into OpenSpec and skips the gate-check question. Without a name: use the branch-matched change, else ask which active change to use, or offer **Create OpenSpec change** when none exists. |
| `--spec <change-name>` | Plans directly from `openspec/changes/<change-name>/`; passed to `plan_prepare` as `fromOpenspec` |
| `--from-openspec <change-name>` | Deprecated alias of `--spec <change-name>` for one release; prints `--from-openspec is deprecated — use --spec <change-name>` |
| `[spec-file-path]` | Requirements file; a path into `openspec/changes/<name>/` selects that change |

- `--auto` never bypasses a Gate A `CRITICAL` verdict.
- `--auto` still asks three questions that have no safe default: which OpenSpec change to use when several match, whether to split into N plans, and what to do after 3 review rounds with open blocking issues. When AskUserQuestion is unavailable, the skill stops and reports instead of choosing.

#### Scenario: Auto mode at the OpenSpec gate check
- **WHEN** `--auto` is set and the gate check finds a functional request with no matching change
- **THEN** the skill does not call AskUserQuestion
- **AND** it takes **Create OpenSpec change** and adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Auto mode with several OpenSpec changes
- **WHEN** `--auto` is set and several active OpenSpec changes exist with no branch match
- **THEN** the skill asks which change to use with AskUserQuestion

#### Scenario: Auto mode resolves an approach question
- **WHEN** `--auto` is set and decomposition reveals two viable approaches
- **THEN** the skill does not call AskUserQuestion
- **AND** it picks the most conservative option, using the closest match to existing codebase patterns as the tiebreaker, and adds a `## Deviations & assumptions` row with `asked=no`

#### Scenario: Deprecated alias
- **WHEN** the skill is invoked with `--from-openspec add-widget`
- **THEN** it behaves as `--spec add-widget`
- **AND** it prints `--from-openspec is deprecated — use --spec <change-name>`

### Requirement: OpenSpec gate check
When `openspec/config.yaml` exists and neither `--spec` nor a path into `openspec/changes/` was given, the skill SHALL classify the request before planning.

| Request | Action |
|---|---|
| Non-functional (refactor, config, docs, CI, deps, test-only) | Print ``OpenSpec detected — pass `--spec` to include spec context in planning.`` and continue without OpenSpec; no question |
| Functional, an active change matches the branch | Treat as `--spec <match>` and load that change |
| Functional, no match | AskUserQuestion with 3 options (below) |

| Option | Effect |
|---|---|
| 1. Create OpenSpec change (default) | Author the change artifacts and stage them (see "OpenSpec artifact staging"); plan from them. `--auto` takes this option without asking. |
| 2. Use existing change | List active changes from `openspec list --json`; load the chosen one |
| 3. Skip OpenSpec | Plan without OpenSpec; ship skips its OpenSpec steps with a reason |

- The question restates what OpenSpec is and what each option costs, per the decision-framing rule.
- No option tells the user to leave plan and run the CLI by hand.

#### Scenario: Functional change, user picks option 1
- **WHEN** the gate asks and the user selects "Create OpenSpec change"
- **THEN** the skill stages the change artifacts and continues planning from them

#### Scenario: Non-functional change
- **WHEN** the request is a refactor and no `--spec` was given
- **THEN** the skill prints ``OpenSpec detected — pass `--spec` to include spec context in planning.``
- **AND** it does not call AskUserQuestion

### Requirement: From-OpenSpec mode
When `fromOpenspec.valid` is true, the skill SHALL read the change's `proposal.md`, `design.md` (optional), every delta spec file listed by `openspec status --change <name> --json` (any depth under `specs/`), and `tasks.md` (optional), set `fromOpenspecDirect = true`, and skip the gate check and complexity routing.

- The plan header gets `**Source:** openspec/changes/<name>/` verbatim; `execute_state` init reads it later.
- `tasks.md` is the primary decomposition skeleton; structured discovery is skipped.
- Each task derived from an OpenSpec task carries an `**openspec-task:**` block with `change`, `ref`, `line`, `title`.
- Every OpenSpec task is covered by a plan task `ref` or listed under `## Out-of-scope OpenSpec tasks`.
- The skill never globs `specs/*.md` itself.

#### Scenario: Invalid change
- **WHEN** `--spec bad-name` is passed and `fromOpenspec.valid` is `false` with errors
- **THEN** the skill displays the errors and stops

#### Scenario: Nested delta specs
- **WHEN** the change has `specs/identity/user-auth/spec.md`
- **THEN** the skill reads that file as a delta spec

## ADDED Requirements

### Requirement: OpenSpec artifact staging
When the user picks **Create OpenSpec change**, the skill SHALL author each artifact in the order of `artifacts` returned by `plan_support({action:"openspec_instructions"})`, using each artifact's `template`, `instruction`, `context`, and `rules` from that result as the template source (not the OpenSpec CLI directly), and SHALL save them only through `plan_support({action:"openspec_stage"})`.

- The skill never writes under `openspec/` and never runs `openspec new change` itself, in plan mode or normal mode.
- `context` and `rules` constrain the content; they are not copied into the artifact.
- When `openspec_stage` returns `valid: false`, the skill fixes the artifacts and stages again, at most 5 times, then shows the CLI output and stops.
- The plan header gets both `**Source:** openspec/changes/<name>/` and `**OpenSpec-Staging:** .sdlc-v2/openspec-staging/<name>/`.
- The `## OpenSpec Appendix` holds only the link to the staging dir and the requirement-to-task table; it never holds spec content.

#### Scenario: Plan mode
- **WHEN** plan mode is active and the user picks **Create OpenSpec change** for `add-widget`
- **THEN** the only files written are the plan file and files under `.sdlc-v2/openspec-staging/add-widget/`

#### Scenario: Validation keeps failing
- **WHEN** `openspec_stage` returns `valid: false` five times in a row
- **THEN** the skill shows the last CLI output and stops without a plan handoff

### Requirement: OpenSpec artifacts follow plan guardrails
The skill SHALL apply the active plan guardrails to OpenSpec artifacts before the guardrail lane runs.
- Create: the skill SHALL treat `guardrails` from `openspec_instructions` as constraints when it authors
  `design` and `tasks`. After Step 6 fixes and before Step 6.5, the skill SHALL rebuild `tasks.md` from the
  final plan tasks and call `openspec_stage` again with all artifacts.
- Existing change: Gate A SHALL receive the guardrails. A task or design decision that conflicts with an
  `error` guardrail is a `WARNING` finding; with a `warning` guardrail, a `SUGGESTION`. Guardrail findings
  never make the verdict `CRITICAL`.

#### Scenario: Create flow re-stages tasks
- **WHEN** the guardrail lane splits plan task 3 into two tasks
- **THEN** the staged `tasks.md` lists both tasks and `stage.json` has a new `validatedAt`

#### Scenario: Existing change breaks a guardrail
- **WHEN** `tasks.md` adds a dependency and guardrail `no-new-deps` (severity `error`) is active
- **THEN** Gate A returns a `WARNING` naming `no-new-deps` and the task line
- **AND** the plan lists it under `## Intake Audit Caveats`

### Requirement: Grouped OpenSpec changes are rejected
When `plan_prepare` reports a grouped change warning, the skill SHALL show the warning message verbatim and SHALL NOT offer the grouped change as an option.

#### Scenario: Grouped change present
- **WHEN** `openspec/changes/grp/demo/` exists
- **THEN** the skill prints the `nested_change_directory` message, which tells the user to rename the change to a flat name such as `grp-demo`
- **AND** `grp` and `grp/demo` are not in the "Use existing change" list

### Requirement: Structured decision recording
Before handoff, the skill SHALL record every `## Key Decisions` entry with one `plan_mark({marker:"criticalDecisions", data:{decisions:[...]}})` call, each entry `{key, choice, rejected, reason}`, where `rejected` lists `{option, why}` for each alternative the plan considered and did not choose.

#### Scenario: Decision with a rejected alternative
- **WHEN** the plan chooses merge over rebase for base sync
- **THEN** the recorded entry is `{key:"base-sync-method", choice:"merge", rejected:[{option:"rebase", why:"rewrites committed wave SHAs"}], reason:"..."}`
