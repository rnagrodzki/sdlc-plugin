# tool-validate Specification

## Purpose
`validate` runs one deterministic validator, picked by `action`, against the project and reports failed checks as findings. Skills and the plan-edit hook use it to check plans, PR templates and bodies, cost tiers, guardrails, review dimensions, CI script drift, and worktree anchoring. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Actions and input fields
The tool SHALL run the validator named by `action` and SHALL use only the input fields listed for that action; other fields are ignored.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `action` | string (enum) | yes | one of the 11 actions below, e.g. `plan_format` | Validator to run |
| `file` | string | `plan_format`, `plan_style` and `plan_gates` only | path, absolute or relative to the root of the action, e.g. `docs/plans/x.md` | Plan file to check |
| `final` | boolean | no | `true` / `false` | `plan_format`: also run PF9, and PF10 when `template` is set |
| `template` | string | no | path, e.g. `.sdlc-v2/plan-template.md` | `plan_format`: template for PF10 and for the PF13 measured sections; `plan_style`: template for the measured sections |
| `strict` | boolean | no | `true` / `false` | `cost_tiers`: report `INHERITED` as `error` instead of `warning` |
| `section` | string | no | config section name, e.g. `execute`; default `plan` | `guardrails`: section to read |
| `activeWorktree` | boolean | no | `true` / `false` | `guardrails`: read the active worktree's config |
| `candidatesJson` | string | no | JSON-encoded array of guardrail entries, e.g. `[{"id":"no-ci-bypass","description":"Plans must not skip CI.","severity":"error"}]` | `guardrails`: proposed entries checked in memory together with the section on disk; nothing is written |
| `body` | string | `pr_body` only | plain text PR body | PR body to check |

| `action` | Inputs used | Root read | Finding IDs |
|---|---|---|---|
| `plan_format` | `file`, `final`, `template` | main | `PF1`–`PF7`, `PF9`, `PF10`, `PF11`, `PF12`, `PF13`, `PF14` |
| `plan_style` | `file`, `template` | main | `PF13` |
| `plan_gates` | `file` | active | `G3`, `G4`, `G8`, `G9`, `G10`, `G12`, `G18` |
| `discovery` | none | main | `PD1`–`PD16` |
| `pr_template` | none | main | `V1`–`V5` |
| `cost_tiers` | `strict` | main | `INHERITED`, `MISSING_DOC`, `DRIFT`, `STALE_DOC`, `NO_COST_DOC` |
| `guardrails` | `section`, `activeWorktree`, `candidatesJson` | main; active with `activeWorktree: true` | the guardrail id |
| `dimensions` | none | active, else main | `D0`–`D13`, `UNKNOWN` |
| `pr_body` | `body` | main | `PR_BODY` |
| `ci_script_drift` | none | main | `CI_SCRIPT_OUTDATED`, `CI_SCRIPT_MISSING` |
| `worktree_anchoring` | none | main and active | `WORKTREE_ANCHOR_BARE`, `WORKTREE_ANCHOR_MISMATCH`, `WORKTREE_ANCHOR_STRAY_STATE` |

- Annotations: `Title: "Validate SDLC artifacts"`, `ReadOnly: true`, `Idempotent: true`, `OpenWorld: false`. No action writes files.
- The tool description names `candidatesJson` and the `fix` field for the `guardrails` action.

#### Scenario: Unused field is ignored
- **WHEN** the tool is called with `action: "discovery"` and `activeWorktree: true`
- **THEN** the findings are the same as without `activeWorktree`

### Requirement: Findings output
The tool SHALL return `findings` for failed checks only; an empty `findings` list means every check passed.

| Field | Meaning |
|---|---|
| `findings[].id` | Check id (see the actions table) |
| `findings[].severity` | `error` or `warning` |
| `findings[].message` | What is wrong |
| `findings[].path` | File the finding is about; empty for some actions |
| `findings[].fix` | Accepted shape, inline (for `guardrails`: the repair step); set on every `plan_format`, `plan_style` and `guardrails` finding, omitted by other actions |
| `worktreeAnchoring` | `worktree_anchoring` only; omitted for other actions |
| `styleReport` | `plan_style` only, returned on every call, pass or fail; omitted for other actions |

#### Scenario: All checks pass
- **WHEN** `action: "guardrails"` runs on a project with no `.sdlc-v2/config.toml`
- **THEN** `findings` is an empty list

### Requirement: Unknown action
The tool SHALL reject an `action` outside the enum with a `DomainError`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Unknown `action` | `DomainError` | `unknown validate action "<action>" (sdlc v<version>, commit <commit>)` / pass one of the valid actions, which the Suggestion lists by name, or update the sdlc plugin |

#### Scenario: Nonsense action
- **WHEN** the tool is called with `action: "nonsense"`
- **THEN** the result is a `DomainError` whose message starts with `unknown validate action "nonsense"`
- **AND** its Suggestion lists `plan_gates`

### Requirement: Root selection
The tool SHALL read from the main worktree root, except for `dimensions`, `ci_script_drift`, `plan_gates`, and opted-in `guardrails`, which read the active worktree root.

| Case | Root | Active root cannot be resolved |
|---|---|---|
| `dimensions` | active | falls back to main (no error) |
| `ci_script_drift` | active | falls back to main (no error) |
| `plan_gates` | active | `InfraError` `resolve active worktree for plan_gates: <cause>` |
| `guardrails` with `activeWorktree: true` | active | `InfraError` `resolve active worktree for guardrails activeWorktree:true: <cause>` |
| Every other case | main | n/a |
| Main root cannot be resolved (any action) | n/a | `InfraError` `resolve project root: <cause>` |

#### Scenario: Guardrails from a linked worktree
- **WHEN** the tool runs from a linked worktree whose `.sdlc-v2/config.toml` has guardrail `Bad_ID` and the main worktree's config does not
- **THEN** `action: "guardrails", activeWorktree: true` reports `Bad_ID`
- **AND** `action: "guardrails"` without the flag does not

#### Scenario: Opt-in with no active worktree fails loud
- **WHEN** the tool runs from the main repo's `.git` directory with `action: "guardrails", activeWorktree: true`
- **THEN** the result is an `InfraError` whose message contains `resolve active worktree`
- **AND** `action: "dimensions"` from the same place succeeds

#### Scenario: CI drift from a linked worktree
- **WHEN** CI files were scaffolded only in a linked worktree and the tool runs there with `action: "ci_script_drift"`
- **THEN** `findings` is an empty list

#### Scenario: plan_gates with no active worktree fails loud
- **WHEN** the tool runs from the main repo's `.git` directory with `action: "plan_gates"`
- **THEN** the result is an `InfraError` whose message starts with `resolve active worktree for plan_gates`

### Requirement: plan_format reads the plan file
The `plan_format` action SHALL require `file`, resolve it against the main worktree root when relative, and report read failures as a `DomainError`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `file` empty | `DomainError` | `plan_format: file is required` / pass the plan path |
| Path does not exist | `DomainError` | `plan_format: file not found: <path>` / check the path; relative resolves against the project root |
| Path exists but cannot be read (e.g. a directory) | `DomainError` | `plan_format: cannot read file: <path>: <cause>` / check it is a regular file with read permission |

#### Scenario: Missing plan file
- **WHEN** `action: "plan_format"` runs with `file: "missing.md"` that does not exist
- **THEN** the result is a `DomainError`

#### Scenario: Directory instead of a file
- **WHEN** `file` points to a directory
- **THEN** the message contains `cannot read`, not `file not found`
- **AND** the suggestion mentions `regular file`

### Requirement: plan_format blocking checks
The `plan_format` action SHALL always run PF1–PF7, PF11, and PF12, and report each failed check as one finding with severity `error`, `path` = the resolved plan path, and a non-empty `fix`.

| ID | Fails when |
|---|---|
| `PF1` | A header field `Goal`, `Architecture`, `Source`, or `Verification` is missing or empty (written as `**Field:** value`) |
| `PF2` | No `### Task N: Title` heading outside code fences; numbering does not start at 0 or 1; a gap or a duplicate number |
| `PF3` | A task lacks `**Complexity:**` (`Trivial`/`Standard`/`Complex`), `**Risk:**` (`Low`/`Medium`/`High`), a non-empty `**Depends on:**`, or `**Verify:**` (see below) |
| `PF4` | `**Depends on:**` names a task that does not exist, or the dependencies form a cycle |
| `PF5` | A task has no `**Acceptance criteria:**` block with at least one `- [ ]`, or its `**Notes:**` block has more than 5 non-blank lines |
| `PF6` | No `## Deviations & assumptions` heading outside code fences |
| `PF7` | A task with a `- Create:`, `- Modify:`, or `- Test:` bullet has no `**Contract:**` line |
| `PF11` | A task lacks a custom field from config key `plan.tasks.requiredFields` |
| `PF12` | Contract block shape does not match config key `plan.tasks.contractShape` |

- `**Verify:**` holds one or more comma-separated values from `tests`, `build`, `lint`, `manual`; each may end with a non-empty scope hint in balanced parentheses, e.g. `tests (go test ./pkg/ -run TestFoo)`. Commas inside parentheses do not split values.
- `**Depends on:**` references are read from the first `Task`/`Tasks` word: digits, commas, `and`, and repeated `Task(s)`. Any other character ends the scan. `none` means no dependencies.
- A cycle is reported as `Circular dependency: Task 1 -> Task 2 -> Task 1`.
- When PF2, PF3, PF4, PF5, PF11, or PF12 lists issues, the message is a headline followed by one `- ` line per issue. PF1 and PF7 join the names with `, `.
- Task headings inside code fences are ignored.
- PF1 and every task-field read (PF3, PF4, and execute's wave computation) SHALL accept a field's value on the same line or on the next non-blank line, and SHALL count the field as empty when that value is itself a `**Label:**` line (any line starting with `**…:**`).

#### Scenario: Empty header field followed by another label
- **WHEN** a plan has `**Goal:**` with nothing after it, and the next line is `**Architecture:** Some arch`
- **THEN** a `PF1` finding names `Goal`
- **AND** the finding does not name `Architecture`

#### Scenario: Header value on the next line
- **WHEN** a plan has `**Goal:**` with nothing after it, and the next line is `Do the thing`
- **THEN** there is no `PF1` finding

#### Scenario: Empty task field followed by another label
- **WHEN** a task has `**Complexity:**` with nothing after it, and the next line is `**Risk:** Low`
- **THEN** a `PF3` finding names `Complexity`
- **AND** the finding does not name `Risk`

#### Scenario: Numbering gap
- **WHEN** a plan has `### Task 1:` and `### Task 4:` only
- **THEN** a `PF2` finding's message contains `gap between Task 1 and Task 4`

#### Scenario: Several metadata issues become a list
- **WHEN** Task 1 has `**Complexity:** Bogus`, `**Risk:** Extreme`, and `**Verify:** flaky`
- **THEN** there is one `PF3` finding whose message has three `\n- Task 1: ` lines

#### Scenario: Depends-on parsing stops at a parenthesis
- **WHEN** a task has `**Depends on:** Task 2 (needs Foo from line 42)` and Task 2 exists
- **THEN** only Task 2 is read as a dependency and PF4 does not report Task 42

#### Scenario: Findings carry path and fix
- **WHEN** a plan misses the `## Deviations & assumptions` section
- **THEN** the `PF6` finding has `path` equal to the resolved plan path
- **AND** its `fix` contains `## Deviations & assumptions` and `| Item | asked | does | why |`

### Requirement: plan_format project task config
The `plan_format` action SHALL read `[plan.tasks]` from `.sdlc-v2/config.toml` for PF11 and PF12.

| Key | Values | Effect |
|---|---|---|
| `requiredFields` | list of field names | PF11 requires each as `**<Field>:** <value>` on every task; names `Complexity`, `Risk`, `Files`, `Verify`, `Depends on` are dropped; empty list passes |
| `contractShape` | `full` (default), `minimal`, `none` | PF12 on tasks with a Create/Modify/Test bullet: `full` needs a `**Contract:**` block with `- shape`, `- names`, `- mirror`, `- decisions`, `- sync`; `minimal` needs the block only; `none` skips PF12 |

- The PF11 message headline is `Missing custom task field(s) required by config key plan.tasks.requiredFields (project-specific, not built in):`.

#### Scenario: Missing custom field
- **WHEN** `requiredFields` is `["Owner"]` and Task 1 has no `**Owner:**`
- **THEN** a `PF11` finding's message contains `Task 1`

#### Scenario: Shallow contract under full and minimal
- **WHEN** a task has `- Create: foo.go` and `**Contract:** does X` with no key bullets
- **THEN** PF12 fails with `contractShape` `full`
- **AND** PF12 passes with `contractShape` `minimal` or `none`

### Requirement: plan_format final checks
When `final` is `true`, the `plan_format` action SHALL also run PF9, and SHALL run PF10 only when `template` is set.

| ID | Fails when |
|---|---|
| `PF9` | No `## Verification Scorecard` heading outside code fences |
| `PF10` | A section listed under the template's `## Required Sections` heading has no matching `## <name>` heading in the plan, outside code fences |

- PF10 reads each `- ` bullet under `## Required Sections` up to the next `##` heading, strips `<!-- narrative: true -->` and `<!-- conditional: ... -->` notes, and checks every listed section.
- A template with no `## Required Sections` heading passes PF10.
- PF10 message: `Missing required section(s) from template <path>: <names>`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `template` path does not exist | `DomainError` | `plan_format: template not found: <path>` / pass the template path, or omit it to skip PF10 |
| `template` path exists but cannot be read | `DomainError` | `plan_format: cannot read template: <path>: <cause>` / check it is a regular file |

#### Scenario: Scorecard missing in final mode
- **WHEN** `final` is `true` and the plan has no `## Verification Scorecard`
- **THEN** a `PF9` finding is reported
- **AND** without `final` no `PF9` finding is reported

#### Scenario: Template section missing
- **WHEN** `final` is `true`, `template` lists `Context` under `## Required Sections`, and the plan has no `## Context`
- **THEN** a `PF10` finding names `Context`

### Requirement: discovery action
The `discovery` action SHALL run the plugin discovery checks `PD1`–`PD16` on the main worktree root. A check skipped because a prerequisite failed produces no finding.

| ID | Checks | Severity |
|---|---|---|
| `PD1` | `.claude-plugin/marketplace.json` exists and is valid JSON | error |
| `PD2` | Marketplace `$schema` field present | warning |
| `PD3` | Marketplace `name` and `plugins` array present | error |
| `PD4` | Each plugin source path has a `plugin.json` | error |
| `PD5` | Marketplace plugin name matches `plugin.json` name | error |
| `PD6` | `plugin.json` has `name`, `description`, `version` | error |
| `PD7` | `version` is valid semver | error |
| `PD8` | Commands have frontmatter with `description` | error |
| `PD9` | Skill names referenced by commands exist | error |
| `PD10` | Scripts referenced by commands exist | error |
| `PD11` | Skills have `SKILL.md` with `name` and `description` | error |
| `PD12` | Sibling `.md` files referenced in `SKILL.md` exist | error |
| `PD13` | Agents referenced by skills exist | error |
| `PD14` | Scripts referenced by skills exist | warning |
| `PD15` | `hooks.json` exists and parses | error |
| `PD16` | Agents have frontmatter with `name`, `description`, `tools` | warning |

#### Scenario: Bare directory
- **WHEN** `action: "discovery"` runs on an empty directory
- **THEN** the call succeeds without an error result

### Requirement: pr_template action
The `pr_template` action SHALL check the PR template file at `.sdlc-v2/pr-template.md`, else `.claude/pr-template.md`. Every finding has severity `error` and `path` = the template path.

| ID | Fails when | Message | Stops later checks |
|---|---|---|---|
| `V1` | No template at either path | `File not found: <relative canonical path>` | yes |
| `V2` | File empty or whitespace only | `File is empty or contains only whitespace` | yes |
| `V3` | No `## ` heading (a `###` line does not count) | `No ## headings found in file` | yes |
| `V4` | A heading name repeats, case-insensitive | `Duplicate: '<Name>' appears <N> times` (joined with `; `) | no |
| `V5` | A section body, trimmed, is under 20 characters | `Section '<Name>' has <N> chars (min 20)` (joined with `; `) | no |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Template exists but cannot be read | `InfraError` | `resolve pr template: <cause>` / check read permission on both paths |

#### Scenario: No template file
- **WHEN** neither template path exists
- **THEN** there is exactly one finding, `V1`

#### Scenario: Duplicate and short sections together
- **WHEN** the template has `## Summary` twice and one section body of 5 characters
- **THEN** both a `V4` and a `V5` finding are reported

### Requirement: pr_body action
The `pr_body` action SHALL report one `PR_BODY` finding, severity `error`, message `missing section: ## <heading>`, for each `## ` heading of the resolved PR template that has no matching `## <heading>` line in `body`.

- No template, or a template with no `## ` headings, gives no findings.
- An empty `body` is not rejected; it reports every template heading as missing.
- Template read failure is an `InfraError` `resolve pr template: <cause>`.

#### Scenario: No template always passes
- **WHEN** no PR template exists and `body` is `anything at all`
- **THEN** `findings` is an empty list

#### Scenario: One section missing
- **WHEN** the template has `## Summary` and `## Testing`, and `body` has only `## Summary`
- **THEN** there is exactly one finding with `id: PR_BODY` and `severity: error`

### Requirement: cost_tiers action
The `cost_tiers` action SHALL compare each skill's and agent's frontmatter `model` with the tables in `docs/cost-tiers.md`.

- Skills: `plugins/sdlc/skills/<dir>/SKILL.md`, else `skills/<dir>/SKILL.md`.
- Agents: `plugins/sdlc/agents/<name>.md`, else `agents/<name>.md`.
- Name comes from frontmatter `name`, else the folder or file name.
- Doc tables: the first table under `## 3. Skill Table` and under `## 4. Agent Table`; column 1 is the name, column 2 the model.

| ID | Severity | Fails when | Message | `path` |
|---|---|---|---|---|
| `INHERITED` | `warning`; `error` with `strict: true` | No `model` in frontmatter | `INHERITED (<skill\|agent>): <name>` | the skill or agent file |
| `MISSING_DOC` | `error` | Model set, name not in the table | `MISSING_DOC (<kind>): <name>=<model>` | the skill or agent file |
| `DRIFT` | `error` | Model differs from the table | `DRIFT (<kind>): <name> frontmatter=<model> doc=<docModel>` | the skill or agent file |
| `STALE_DOC` | `error` | Table row with no matching skill or agent | `STALE_DOC (<kind>): <name>` | `docs/cost-tiers.md` |

#### Scenario: All four kinds
- **WHEN** skills `drift-skill` (haiku vs doc opus), `missing-doc-skill` (not in doc), `inherited-skill` (no model) exist and the doc lists `stale-skill`
- **THEN** there is one `DRIFT`, one `MISSING_DOC`, one `STALE_DOC`, and one `INHERITED` finding
- **AND** `INHERITED` has severity `warning`

#### Scenario: Strict mode
- **WHEN** the same project is checked with `strict: true`
- **THEN** the `INHERITED` finding has severity `error`

#### Scenario: Plugin layout
- **WHEN** `plugins/sdlc/skills/plan/SKILL.md` has `model: haiku`, `plugins/sdlc/agents/helper.md` has `model: sonnet`, and the doc lists `plan` as `opus` and `helper` as `sonnet`
- **THEN** there is one `DRIFT` finding for `plan`
- **AND** there is no `STALE_DOC` finding

### Requirement: cost_tiers missing doc
The `cost_tiers` action SHALL skip the comparison when `docs/cost-tiers.md` does not exist and SHALL return exactly one finding that says so, not an error.

| ID | Severity | Message | `path` |
|---|---|---|---|
| `NO_COST_DOC` | `warning` | `NO_COST_DOC: no cost-tier doc exists, so the cost_tiers check was skipped` | `docs/cost-tiers.md` |

#### Scenario: Missing doc
- **WHEN** `docs/cost-tiers.md` does not exist and a skill with `model: opus` exists
- **THEN** the result is not an error
- **AND** `findings` has exactly one finding with `id: NO_COST_DOC` and `severity: warning`

### Requirement: cost_tiers doc errors
The `cost_tiers` action SHALL return a `DataError` when `docs/cost-tiers.md` exists but is unreadable, or its tables are malformed. The message SHALL name the file once.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Doc exists but is unreadable (e.g. a directory, no read permission) | `DataError` | `cost-tier tables: read cost-tier doc: <cause>` / check read permission |
| Heading or table missing or empty | `DataError` | `cost-tier tables: cost-tiers.md: table not found or empty (heading "## 3. Skill Table")` (or Agent) / fix the two headings and the rows below them |
| Row pipe count differs from the header | `DataError` | `cost-tier tables: docs/cost-tiers.md: row <N> has <P> pipes, expected <Q>: <line>` / same as above |

#### Scenario: Unreadable doc
- **WHEN** `docs/cost-tiers.md` is a directory
- **THEN** the result is a `DataError` whose suggestion contains `check read permission` and not `headings`

#### Scenario: Doc without tables
- **WHEN** `docs/cost-tiers.md` has no tables
- **THEN** the result is a `DataError` whose suggestion contains `## 3. Skill Table`

### Requirement: guardrails action
The `guardrails` action SHALL check each guardrail in `[<section>.guardrails.<id>]` of `.sdlc-v2/config.toml` (default `section` is `plan`) and report every problem as a finding with severity `error`, `id` = the guardrail id, and message `<id>: <problem>`.

| Problem text | Trigger |
|---|---|
| `id is missing` / `id must be a string` | No usable id (id shown as `(missing)`): an array-form entry with no `id`, a non-string `id`, or an empty table key `[<section>.guardrails.""]` |
| `id must match kebab-case pattern: /^[a-z][a-z0-9]*(-[a-z0-9]+)*$/` | Id is not kebab-case |
| `id is duplicated across guardrails` | Same id seen twice (only possible in the array form) |
| `description is missing` | No description, or an empty string |
| `description must be a string` | Description is not a string |
| `description cannot be empty` | Description is only whitespace |
| `description exceeds 1024 bytes (<N> bytes, <N-1024> over)` | Description longer than 1024 bytes |
| `severity must be "error", "warning", or undefined (got "<value>")` | `severity` set to any other value |

- A missing config file, missing section, or section without guardrails gives no findings for the file; entries from `candidatesJson` are still checked.
- A config read failure is an `InfraError` `read <section> guardrails section: <cause>` / check the section is valid TOML. This holds with and without `candidatesJson`.

#### Scenario: Mixed guardrails
- **WHEN** the `plan` section has `good-guardrail`, `Bad_ID`, `sev-bad` (`severity = "critical"`), and `no-desc` (no description)
- **THEN** there are 3 findings, one each for `Bad_ID`, `sev-bad`, and `no-desc`, all severity `error`

#### Scenario: Custom section
- **WHEN** `section` is `execute` and `[execute.guardrails.exec-guardrail]` has `description = ""`
- **THEN** there is exactly one finding

#### Scenario: Array form with missing and duplicate ids
- **WHEN** the `plan` section uses `[[plan.guardrails]]` with one entry that has no `id` and two entries with `id = "dup-id"`
- **THEN** there are 2 findings: `(missing): id is missing` and `dup-id: id is duplicated across guardrails`

#### Scenario: Empty table key
- **WHEN** the `plan` section has `[plan.guardrails.""]` with a description
- **THEN** there is exactly one finding, `(missing): id is missing`

#### Scenario: Description over the byte limit
- **WHEN** guardrail `dry` has a description of 1310 bytes
- **THEN** there is one finding with message `dry: description exceeds 1024 bytes (1310 bytes, 286 over)`

#### Scenario: Description at the byte limit
- **WHEN** guardrail `dry` has a description of exactly 1024 bytes
- **THEN** there is no length finding for `dry`

#### Scenario: Malformed config file
- **WHEN** `.sdlc-v2/config.toml` holds invalid TOML
- **THEN** the result is an `InfraError` whose message starts with `read plan guardrails section:` and that has a `Suggestion`
- **AND** the result is the same when `candidatesJson` is set

### Requirement: dimensions action
The `dimensions` action SHALL validate every review-dimension file in `.sdlc-v2/review-dimensions/` of the active worktree and map each problem to an id and severity. `path` is the dimension file name.

| ID | Severity | Problem |
|---|---|---|
| `D0` | error | `Cannot read file: ...` |
| `D1` | error | Missing YAML frontmatter block |
| `D2` | error | `name` missing, not a string, not lowercase-hyphen, or over 64 characters |
| `D3` | error | `description` missing, not a string, empty, or over 256 characters |
| `D4` | error | `triggers` missing, not a string list, or empty |
| `D5` | error | Invalid glob in `triggers` |
| `D6` | warning | `severity` not an allowed value |
| `D7` | warning | `max-files` not a positive integer |
| `D8` | warning | `skip-when` not a string list, or invalid glob in it |
| `D9` | error | Body under 10 characters |
| `D10` | error | Same `name` used by an earlier file: `Duplicate dimension name "<name>" — also used in <file>` |
| `D11` | warning | Unknown frontmatter field |
| `D12` | warning | `requires-full-diff` not a boolean |
| `D13` | warning | `model` not a non-empty string |
| `UNKNOWN` | error | Any other problem text |

- A missing `.sdlc-v2/review-dimensions/` folder gives no findings.
- A folder that exists but cannot be listed returns `InfraError` `load review dimensions: <cause>` / check filesystem permissions on `.sdlc-v2/review-dimensions/`, then retry.

#### Scenario: Valid dimension
- **WHEN** one file has a valid `name`, `description`, `triggers`, and a long enough body
- **THEN** `findings` is an empty list

#### Scenario: Missing description and unknown field
- **WHEN** a file has no `description` and an extra field `randomfield`
- **THEN** there is a `D3` finding with message `Missing required field: description` and severity `error`
- **AND** a `D11` finding with severity `warning` that mentions `"randomfield"`

#### Scenario: Duplicate name across files
- **WHEN** `a-dim.md` and `b-dim.md` both use name `security-review`
- **THEN** there is one `D10` finding with `path: b-dim.md`
- **AND** message `Duplicate dimension name "security-review" — also used in a-dim.md`

#### Scenario: Dimensions folder cannot be listed
- **WHEN** `.sdlc-v2/review-dimensions` is a regular file, not a directory
- **THEN** the tool returns `InfraError` starting with `load review dimensions:`

### Requirement: ci_script_drift action
The `ci_script_drift` action SHALL compare each CI file installed by `scaffold_ci` with the version `scaffold_ci` would install and report non-current files as `warning` findings. It SHALL NOT write files.

| State | ID | Message |
|---|---|---|
| Current | none | none |
| Installed version lower, or only the legacy `.js` file exists | `CI_SCRIPT_OUTDATED` | `<path> is outdated (installed v<I>, current v<C>) — run scaffold_ci({force:true}) to update.` |
| Not installed | `CI_SCRIPT_MISSING` | `<path> is not installed (current v<C>) — run scaffold_ci({force:true}) to install.` |

- `path` is the file path relative to the project root, e.g. `.github/workflows/release-on-main.yml`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Embedded payload missing | `InfraError` | `embedded payload "<name>" not found` / update or reinstall the plugin |
| Installed file cannot be read | `InfraError` | `check installed version of CI script <path>: read <file>: <cause>` / check read permission |

#### Scenario: Freshly scaffolded project
- **WHEN** `scaffold_ci` has just run
- **THEN** `findings` is an empty list

#### Scenario: One outdated, one missing
- **WHEN** `.github/workflows/release-on-main.yml` is replaced with `# release-on-main-version: 1` and `.github/workflows/check-changelog.yml` is deleted
- **THEN** there is one `CI_SCRIPT_OUTDATED` and one `CI_SCRIPT_MISSING` finding
- **AND** both messages contain `scaffold_ci({force:true})`

### Requirement: worktree_anchoring action
The `worktree_anchoring` action SHALL return `worktreeAnchoring` and report how the `.sdlc-v2/` state directory is anchored.

| Output field | Meaning |
|---|---|
| `worktreeAnchoring.mainRoot` | Main worktree root |
| `worktreeAnchoring.activeRoot` | Active worktree root |
| `worktreeAnchoring.isLinked` | Main and active roots differ after resolving symlinks |
| `worktreeAnchoring.isBare` | Main root is a bare repository |
| `worktreeAnchoring.stateDir` | `<main>/.sdlc-v2` if it exists; else `<active>/.sdlc-v2` if it exists; else `<main>/.sdlc-v2` |
| `worktreeAnchoring.stateDirOwner` | `main` or `active`, matching `stateDir` |

| ID | Severity | Fails when | `path` |
|---|---|---|---|
| `WORKTREE_ANCHOR_BARE` | error | Main root is a bare repository | main root |
| `WORKTREE_ANCHOR_MISMATCH` | warning | `.sdlc-v2/` exists under the active root but not the main root | `stateDir` |
| `WORKTREE_ANCHOR_STRAY_STATE` | error | Linked worktree only: an entry in `<active>/.sdlc-v2/` other than `.gitignore`, `config.toml`, `review-dimensions`; message `<entry> exists in the linked worktree; state belongs in <main>/.sdlc-v2/` | the entry path |

| Condition | Class | Message (short) |
|---|---|---|
| Active root cannot be resolved | `InfraError` | `resolve active worktree: <cause>` |
| Bare status cannot be read | `InfraError` | `determine bare status: <cause>` |
| `.sdlc-v2` cannot be checked | `InfraError` | `resolve state dir owner: <cause>` |
| Linked `.sdlc-v2/` cannot be listed | `InfraError` | `scan linked worktree state: <cause>` |

#### Scenario: Stray state in a linked worktree
- **WHEN** the linked worktree has `.sdlc-v2/reports/`
- **THEN** there is one `WORKTREE_ANCHOR_STRAY_STATE` finding with severity `error` whose message names `reports`

#### Scenario: Only tracked entries
- **WHEN** the linked worktree's `.sdlc-v2/` holds only `.gitignore`, `config.toml`, and `review-dimensions/`
- **THEN** no `WORKTREE_ANCHOR_STRAY_STATE` finding is reported

#### Scenario: Linked worktree without state directory
- **WHEN** the linked worktree has no `.sdlc-v2/`
- **THEN** no stray-state finding is reported and the call does not fail

### Requirement: plan_format style check
The `plan_format` action SHALL run PF13 on every call and SHALL report all PF13 failures as one `error` finding: a headline, one `- ` line per failure, and a non-empty `fix`. Limits come from capability `plan-writing-style`; the PostToolUse plan hook SHALL NOT run PF13.

| Metric | How it is counted | Fails when |
|---|---|---|
| prose share | prose words / all words in a measured section; table rows, fenced-block lines, and list items of 30 words or fewer are visual | above `maxProseShare` |
| paragraph length | sentences in one run of consecutive prose lines | above `maxParagraphSentences` |
| list length | items in one unbroken list | above `maxListItems` |
| sentence length | words in each prose or list sentence | more than 10% of sentences above `maxSentenceWords` |
| jargon share | prose sentences with inline code or a `dir/file.ext` token / all prose sentences | above `maxJargonShare` |
| banned phrase | case-insensitive match outside code | any match |
| STE rule (only `writingStandard: ste`) | strict STE checks per capability `plan-writing-style` | any hit; line `line <N>: STE <rule id>: "<text>"` |

- Measured sections: sections marked `<!-- narrative: true -->` in `template` when set; otherwise `Context`, `Research Findings`, `Key Decisions`, `Final Shape`.
- A measured section with fewer than 40 words is skipped. The banned-phrase and STE checks read the whole plan.

#### Scenario: Too much prose at high density
- **WHEN** `visualDensity` is `high` and the `Context` section is 60% prose words
- **THEN** one `PF13` finding has a line that names `Context` and `prose share 0.60 > 0.30`

#### Scenario: Banned phrase
- **WHEN** `tone` is `direct` and the plan contains `Great question`
- **THEN** one `PF13` finding has a line that contains `great question`

#### Scenario: STE hit
- **WHEN** `writingStandard` is `ste` and line 92 of the plan says `Before starting the tool, read the guide.`
- **THEN** one `PF13` finding has the line `line 92: STE ing-form: "starting"`

#### Scenario: Short section is skipped
- **WHEN** the `Final Shape` section has 25 words of prose
- **THEN** no PF13 line names `Final Shape`

#### Scenario: Hook does not run PF13
- **WHEN** the PostToolUse plan hook checks a plan whose `Context` breaks the prose-share limit
- **THEN** the hook reports no `PF13` finding

### Requirement: plan_format diagram contrast check
The `plan_format` action SHALL run PF14 on the whole plan on every call. The PostToolUse plan hook SHALL run PF14 only on the new text of the edit (`Edit` `new_string`, every `MultiEdit` `edits[].new_string`, or `Write` `content`). PF14 SHALL report all diagram contrast hits (capability `plan-writing-style`, "Diagram contrast") as one `error` finding: a headline, one `- line <N>: <reason>: <text>` line per hit, and a `fix` that quotes the two exact classDefs.

#### Scenario: Hook blocks a pastel classDef
- **WHEN** an `Edit` of a plan file has a `new_string` with the line `classDef new fill:#d4f7d4,stroke:#2a7a2a`
- **THEN** the hook reports one `PF14` finding with a line that ends `no text color: classDef new fill:#d4f7d4,stroke:#2a7a2a`

#### Scenario: Unrelated edit of an older plan passes
- **WHEN** a plan file has an older pastel classDef and an `Edit` changes only a typo in another line
- **THEN** the hook reports no `PF14` finding

#### Scenario: plan_format checks the whole file
- **WHEN** `plan_format` runs on a plan with a pastel classDef inside a mermaid block at line 120
- **THEN** one `PF14` finding has the line `- line 120: no text color: classDef new fill:#d4f7d4,stroke:#2a7a2a`

#### Scenario: Shipped classes pass
- **WHEN** every mermaid classDef in the plan is one of the two exact lines
- **THEN** there is no `PF14` finding

### Requirement: plan_style action
The `plan_style` action SHALL return `styleReport` on every call and SHALL return the PF13 failures as `findings`. It requires `file`; `template` is optional.

| `styleReport` field | Meaning |
|---|---|
| `settings` | `audience`, `writingStandard`, `tone`, `visualDensity`, `language` in effect |
| `limits` | the numeric limits in effect |
| `sections[]` | `name`, `words`, `proseShare`, `longestParagraph`, `longestList`, `longSentenceShare`, `jargonShare`, `status` (`pass`, `fail`, or `skipped`) |
| `bannedPhraseHits[]` | `phrase`, `line` |
| `steHits[]` | `rule`, `text`, `line`; empty when `writingStandard` is not `ste` |
| `diagramContrast[]` | `line`, `text`, `reason`; the same hits as PF14 |
| `instructions[]` | the custom plan instructions, read fresh, for the handoff self-check |
| `warnings` | style warnings from the settings |

- Every list field is `[]` when empty, never null.

#### Scenario: Passing plan still returns a report
- **WHEN** `plan_style` runs on a plan that meets every limit
- **THEN** `findings` is an empty list
- **AND** `styleReport.sections` has one row per measured section with `status: pass`

#### Scenario: Instructions in the report
- **WHEN** `[planStyle] instructions` is `["A"]`
- **THEN** `styleReport.instructions` is `["A"]`

#### Scenario: Missing file input
- **WHEN** `plan_style` runs without `file`
- **THEN** the tool returns a `DomainError` whose suggestion says to pass the plan file path in `file`

### Requirement: guardrails candidates checked in memory
With `candidatesJson`, the `guardrails` action SHALL check the proposed entries together with the section on disk, in memory, and SHALL write nothing.

- A candidate whose `id` equals a disk entry's id replaces that entry for the check.
- A candidate with a new `id` is added; all entries are checked.
- Without `candidatesJson`, the action checks only the disk entries.
- `candidatesJson` that is not a JSON array of objects returns a `DomainError` with a `Suggestion`; no entry is checked.

#### Scenario: Candidate replaces a disk entry
- **WHEN** `[plan.guardrails.dry]` on disk has a 1310-byte description
- **AND** `candidatesJson` is `[{"id":"dry","description":"Reuse existing helpers.","severity":"error"}]`
- **THEN** there is no finding for `dry`
- **AND** `.sdlc-v2/config.toml` is unchanged

#### Scenario: New candidate added
- **WHEN** the disk section has `good-guardrail`
- **AND** `candidatesJson` is `[{"id":"Bad_ID","description":"x","severity":"error"}]`
- **THEN** there is one finding, for `Bad_ID`

#### Scenario: No config file
- **WHEN** the project has no `.sdlc-v2/config.toml`
- **AND** `candidatesJson` holds one entry with `severity` `critical`
- **THEN** there is one finding, for that entry

#### Scenario: Malformed candidatesJson
- **WHEN** `candidatesJson` is `[{"id":`
- **THEN** the result is a `DomainError` with a `Suggestion`
- **AND** no finding is returned

### Requirement: guardrails finding fix hints
Every `guardrails` finding SHALL carry a non-empty `fix` text that tells the caller how to repair the entry.

- The length, id-format, duplicate-id and severity texts are pinned in the scenarios below.

#### Scenario: Length finding fix
- **WHEN** guardrail `x` has a description of 1310 bytes
- **THEN** the finding's `fix` is `Shorten the description to 1024 bytes or less, or split it into independent guardrails with ids x-1, x-2, each a complete rule.`

#### Scenario: Id format finding fix
- **WHEN** guardrail `Bad_ID` exists
- **THEN** the finding's `fix` is `Rename the id to lowercase words joined by single hyphens, e.g. no-ci-bypass.`

#### Scenario: Duplicate id finding fix
- **WHEN** two `[[plan.guardrails]]` entries have `id = "dup-id"`
- **THEN** the finding's `fix` is `Use action consolidate on the current id, or pick a new unique id.`

#### Scenario: Severity finding fix
- **WHEN** guardrail `sev-bad` has `severity = "critical"`
- **THEN** the finding's `fix` is `Set severity to error or warning.`

#### Scenario: Every finding has a fix
- **WHEN** the `plan` section has `Bad_ID`, `sev-bad` and `no-desc`
- **THEN** each of the 3 findings has a non-empty `fix`

### Requirement: Guardrail severity downgrade
The `guardrails` action SHALL return one error finding for each candidate in `candidatesJson` that lowers the severity of a disk entry with the same id. A disk entry with no severity counts as `error`.

#### Scenario: Error to warning
- **WHEN** the disk entry `no-ci-bypass` has severity `error` and a candidate with the same id has severity `warning`
- **THEN** the findings include `no-ci-bypass: severity lowered from error to warning (harden never lowers a severity)`
- **AND** the fix is `Keep severity error, or propose a new guardrail id for the weaker rule.`

#### Scenario: No candidates
- **WHEN** `candidatesJson` is empty
- **THEN** the findings are the same as before this change

### Requirement: Guardrail check scope
The `guardrails` action SHALL report findings only for the ids in `scopeIds` when it is set, else for the candidate ids when `candidatesJson` is set, else for every id. Severity-downgrade findings of candidates SHALL always be reported.

#### Scenario: Old finding outside the scope
- **WHEN** the section holds an oversize entry `old`
- **AND** `candidatesJson` holds one clean entry `new`
- **THEN** the findings are empty

#### Scenario: Empty scope
- **WHEN** `scopeIds` is `[]`
- **THEN** the tool returns a DomainError `scopeIds is empty`

#### Scenario: Unknown scope id
- **WHEN** `scopeIds` names `x` and `x` is not on disk and not in the candidates
- **THEN** the tool returns a DomainError `scopeIds names x, which is not in section plan`

### Requirement: Remove candidates
A candidate `{"id":"x","remove":true}` SHALL drop disk entry `x` from the in-memory check. A remove candidate SHALL set no other field, and entry `x` SHALL exist on disk.

#### Scenario: Remove an entry
- **WHEN** the disk has oversize entry `x`
- **AND** `candidatesJson` is `[{"id":"x","remove":true}]`
- **THEN** the findings are empty

#### Scenario: Remove a missing entry
- **WHEN** `candidatesJson` is `[{"id":"y","remove":true}]` and no entry `y` is on disk
- **THEN** the tool returns a DomainError `remove candidate y has no entry on disk`

### Requirement: Dimensions file scope
The `dimensions` action with `file` SHALL report the findings of the dimension file whose base name matches, plus the cross-file duplicate-name findings that name it. A `file` that matches no dimension file SHALL return a DomainError.

#### Scenario: One file
- **WHEN** `file` is `.sdlc-v2/review-dimensions/a.md` and `b.md` has a finding
- **THEN** the findings hold no entry for `b.md`

#### Scenario: No match
- **WHEN** `file` is `missing.md`
- **THEN** the tool returns a DomainError `file missing.md matches no review-dimension file`

### Requirement: plan_gates action
The `plan_gates` action SHALL check a plan file against the gates G3, G4, G8, G9, G12 and the G18 floor. It SHALL return one finding for each offender, with the gate id as `id` and the plan file as `path`, sorted by gate, then task number, then message. G8 and G9 findings SHALL be `warning`; the other gates SHALL be `error`, except the G10 Files-bullet warnings. It requires `file`, resolves a relative `file` against the active worktree root, and writes nothing.

#### Scenario: Forward reference
- **WHEN** Task 2 depends on Task 3
- **THEN** the findings hold the `error` finding G3 `Task 2: depends on Task 3, which is not listed before it`

#### Scenario: Self-dependency
- **WHEN** Task 2 depends on Task 2
- **THEN** the findings hold the `error` finding G3 `Task 2: depends on itself`

#### Scenario: Missing dependency target
- **WHEN** Task 2 depends on Task 9 and the plan has no Task 9
- **THEN** the findings hold the `error` finding G12 `Task 2: depends on nonexistent Task 9`

#### Scenario: Shared path with a dependency chain
- **WHEN** Tasks 1 and 3 modify one path, Task 3 depends on Task 2, and Task 2 depends on Task 1
- **THEN** the findings hold no G4 finding

#### Scenario: Shared path with no dependency
- **WHEN** Task 1 modifies `./a.go`, Task 2 modifies `a.go`, and no dependency links them
- **THEN** the findings hold one `error` finding G4 `Task 1 and Task 2 both modify a.go with no dependency path between them`

#### Scenario: Missing Verify field
- **WHEN** Task 1 has no `**Verify:**` line
- **THEN** the findings hold a `warning` finding G8 whose message is `Task 1` and the PF3 Verify text

#### Scenario: Too many files
- **WHEN** Task 1 touches 6 distinct paths
- **THEN** the findings hold the `warning` finding G9 `Task 1: touches 6 files (limit 5)`

#### Scenario: Too many Trivial tasks
- **WHEN** 5 of 5 tasks are Trivial
- **THEN** the findings hold the `warning` finding G9 `Plan: 5 of 5 tasks are Trivial (limit 80%)`

#### Scenario: Prose-only Contract
- **WHEN** the Contract block of the last task has prose only, and a `## Verification Scorecard` table follows it
- **THEN** the findings hold the `error` finding G18 `Task N: Contract has no code block, table or diagram` for that task

#### Scenario: No Contract line
- **WHEN** a task has no `**Contract:**` line
- **THEN** the findings hold no G18 finding for that task

#### Scenario: Not a plan
- **WHEN** the file has no `### Task N:` heading outside a code fence
- **THEN** the result is a `DomainError` whose message starts with `plan_gates: no "### Task N: <title>" heading outside a code fence, so no gate was checked:`
- **AND** its Suggestion ends with `Then retry plan_gates.`

#### Scenario: G18 floor outside the go-gates gate IDs
- **WHEN** a task has a prose-only Contract
- **THEN** the finding carries id `G18`, although G18 is not in the `go-gates` gate IDs of `plan_prepare`

#### Scenario: Dot slash path is one path
- **WHEN** Task 1 modifies `./a.go` and `a.go` in two bullets
- **THEN** the action reads one Modify path `a.go`

#### Scenario: Extension path wins over a slash word
- **WHEN** a Files bullet is `- Modify: the \`read/write\` helper in \`a.go\``
- **THEN** the Modify path of that bullet is `a.go`

#### Scenario: Files bullet with no path
- **WHEN** Task 1 has the Files bullet `- Modify: the helpers`
- **THEN** the findings hold a `warning` finding G10 that starts with `Task 1: Files bullet "Modify: the helpers" names no path, so G4, G9 and G10 skip it. Write it as`

#### Scenario: Task with no Files block
- **WHEN** Task 3 has no `**Files:**` line
- **THEN** the findings hold the `warning` finding G10 `Task 3: no **Files:** block, so G4, G9 and G10 do not check this task`

#### Scenario: Plan file not found
- **WHEN** `plan_gates` runs with a `file` that does not exist
- **THEN** the result is a `DomainError` whose Suggestion ends with `A relative path resolves against the active worktree root (the checkout of the current branch).`
- **AND** the same call with `plan_format` gives a Suggestion that ends with `A relative path resolves against the main worktree root.`

#### Scenario: Empty file input
- **WHEN** `plan_gates` runs with an empty `file`
- **THEN** the result is a `DomainError` `plan_gates: file is required`

### Requirement: plan_gates Modify path check
The `plan_gates` action SHALL check each Modify path against the active worktree root and report each bad path as a G10 `error` finding. A path that the plan text makes impossible to check (a parent is a file, a name too long, a symbolic link loop) SHALL be a G10 `error` finding, so the other gates still report. Any other failed check of a Modify path SHALL return an `InfraError`.

#### Scenario: Absent path
- **WHEN** Task 2 modifies `x.go`, `x.go` does not exist, and no earlier task creates it
- **THEN** the findings hold the G10 finding `Task 2: Modify path x.go does not exist`

#### Scenario: Path created by an earlier task
- **WHEN** Task 1 creates `x.go`, Task 2 modifies `x.go`, and `x.go` does not exist
- **THEN** the findings hold no G10 finding

#### Scenario: Folder path
- **WHEN** a Modify path of Task 2 is a folder
- **THEN** the findings hold a G10 finding that ends with `is not a regular file`

#### Scenario: Path outside the project
- **WHEN** a Modify path of Task 2 is `../x.go`
- **THEN** the findings hold a G10 finding that ends with `is outside the project`

#### Scenario: Parent is a file
- **WHEN** a Modify path of Task 1 is `a.txt/x.go` and `a.txt` is a regular file
- **THEN** the findings hold the `error` finding G10 `Task 1: Modify path a.txt/x.go cannot be checked: a parent in the path is a file, not a folder`
- **AND** the findings of the other gates are still returned

#### Scenario: Name too long
- **WHEN** a Modify path of Task 1 has a name longer than the file system allows
- **THEN** the findings hold a G10 finding that ends with `cannot be checked: the path or one of its names is too long`

#### Scenario: Permission denied
- **WHEN** a Modify path is `locked/x.go` and the process has no search permission on `locked`
- **THEN** the result is an `InfraError` whose message starts with `plan_gates: cannot check Modify path locked/x.go: ` and whose cause is the permission error
- **AND** its Suggestion is `Give this process read and search permission on each folder of locked/x.go (ls -ld on each parent shows it). Then retry plan_gates.`

#### Scenario: Create and Modify in one task
- **WHEN** Task 1 creates and modifies `new.go`, and `new.go` does not exist
- **THEN** the findings hold the G10 finding `Task 1: Modify path new.go does not exist`

#### Scenario: Path only in the active worktree
- **WHEN** a Modify path exists in the active worktree and not in the main worktree
- **THEN** the findings hold no G10 finding

### Requirement: Plan file read limit
The `plan_format`, `plan_style` and `plan_gates` actions SHALL refuse a plan file over 8 MiB (8388608 bytes) with a `DomainError` that names the action, the size and the limit, and SHALL read a file of exactly 8 MiB.

#### Scenario: File over the limit
- **WHEN** `plan_gates` runs on a file of 8388609 bytes
- **THEN** the result is a `DomainError` `plan_gates: file is 8388609 bytes, over the 8 MiB limit (8388608 bytes): <path>`

#### Scenario: File at the limit
- **WHEN** `plan_format` runs on a file of 8388608 bytes
- **THEN** the action reads the file and returns its findings
