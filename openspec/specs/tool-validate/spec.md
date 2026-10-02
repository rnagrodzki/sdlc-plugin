# tool-validate Specification

## Purpose
`validate` runs one deterministic validator, picked by `action`, against the project and reports failed checks as findings. Skills and the plan-edit hook use it to check plans, PR templates and bodies, cost tiers, guardrails, review dimensions, CI script drift, and worktree anchoring. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Actions and input fields
The tool SHALL run the validator named by `action` and SHALL use only the input fields listed for that action; other fields are ignored.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `action` | string (enum) | yes | one of the 9 actions below, e.g. `plan_format` | Validator to run |
| `file` | string | `plan_format` only | path, absolute or relative to the main worktree root, e.g. `docs/plans/x.md` | Plan file to check |
| `final` | boolean | no | `true` / `false` | `plan_format`: also run PF9, and PF10 when `template` is set |
| `template` | string | no | path, e.g. `.sdlc-v2/plan-template.md` | `plan_format`: template for PF10; omit to skip PF10 |
| `strict` | boolean | no | `true` / `false` | `cost_tiers`: report `INHERITED` as `error` instead of `warning` |
| `section` | string | no | config section name, e.g. `execute`; default `plan` | `guardrails`: section to read |
| `activeWorktree` | boolean | no | `true` / `false` | `guardrails`: read the active worktree's config |
| `body` | string | `pr_body` only | plain text PR body | PR body to check |

| `action` | Inputs used | Root read | Finding IDs |
|---|---|---|---|
| `plan_format` | `file`, `final`, `template` | main | `PF1`–`PF7`, `PF9`, `PF10`, `PF11`, `PF12` |
| `discovery` | none | main | `PD1`–`PD16` |
| `pr_template` | none | main | `V1`–`V5` |
| `cost_tiers` | `strict` | main | `INHERITED`, `MISSING_DOC`, `DRIFT`, `STALE_DOC`, `NO_COST_DOC` |
| `guardrails` | `section`, `activeWorktree` | main; active with `activeWorktree: true` | the guardrail id |
| `dimensions` | none | active, else main | `D0`–`D13`, `UNKNOWN` |
| `pr_body` | `body` | main | `PR_BODY` |
| `ci_script_drift` | none | main | `CI_SCRIPT_OUTDATED`, `CI_SCRIPT_MISSING` |
| `worktree_anchoring` | none | main and active | `WORKTREE_ANCHOR_BARE`, `WORKTREE_ANCHOR_MISMATCH`, `WORKTREE_ANCHOR_STRAY_STATE` |

- Annotations: `Title: "Validate SDLC artifacts"`, `ReadOnly: true`, `Idempotent: true`, `OpenWorld: false`. No action writes files.

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
| `findings[].fix` | Accepted shape, inline; set on every `plan_format` finding, omitted by other actions |
| `worktreeAnchoring` | `worktree_anchoring` only; omitted for other actions |

#### Scenario: All checks pass
- **WHEN** `action: "guardrails"` runs on a project with no `.sdlc-v2/config.toml`
- **THEN** `findings` is an empty list

### Requirement: Unknown action
The tool SHALL reject an `action` outside the enum with a `DomainError`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Unknown `action` | `DomainError` | `unknown validate action "<action>" (sdlc v<version>, commit <commit>)` / pass one of the 9 valid actions, or update the sdlc plugin |

#### Scenario: Nonsense action
- **WHEN** the tool is called with `action: "nonsense"`
- **THEN** the result is a `DomainError` whose message starts with `unknown validate action "nonsense"`

### Requirement: Root selection
The tool SHALL read from the main worktree root, except for `dimensions`, `ci_script_drift`, and opted-in `guardrails`, which read the active worktree root.

| Case | Root | Active root cannot be resolved |
|---|---|---|
| `dimensions` | active | falls back to main (no error) |
| `ci_script_drift` | active | falls back to main (no error) |
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
| `description exceeds 1024 characters (<N> chars)` | Description longer than 1024 characters |
| `severity must be "error", "warning", or undefined (got "<value>")` | `severity` set to any other value |

- A missing config file, missing section, or section without guardrails gives no findings.
- A config read failure is an `InfraError` `read <section> guardrails section: <cause>` / check the section is valid TOML.

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
