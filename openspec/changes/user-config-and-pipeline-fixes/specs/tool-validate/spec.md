# Spec Delta

## MODIFIED Requirements

### Requirement: Actions and input fields
The tool SHALL run the validator named by `action` and SHALL use only the input fields listed for that action; other fields are ignored.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `action` | string (enum) | yes | one of the 10 actions below, e.g. `plan_format` | Validator to run |
| `file` | string | `plan_format` and `plan_style` only | path, absolute or relative to the main worktree root, e.g. `docs/plans/x.md` | Plan file to check |
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

## ADDED Requirements

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
