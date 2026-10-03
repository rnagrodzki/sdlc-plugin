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
| `body` | string | `pr_body` only | plain text PR body | PR body to check |

| `action` | Inputs used | Root read | Finding IDs |
|---|---|---|---|
| `plan_format` | `file`, `final`, `template` | main | `PF1`–`PF7`, `PF9`, `PF10`, `PF11`, `PF12`, `PF13`, `PF14` |
| `plan_style` | `file`, `template` | main | `PF13` |
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
| `findings[].fix` | Accepted shape, inline; set on every `plan_format` and `plan_style` finding, omitted by other actions |
| `worktreeAnchoring` | `worktree_anchoring` only; omitted for other actions |
| `styleReport` | `plan_style` only, returned on every call, pass or fail; omitted for other actions |

#### Scenario: All checks pass
- **WHEN** `action: "guardrails"` runs on a project with no `.sdlc-v2/config.toml`
- **THEN** `findings` is an empty list

## ADDED Requirements

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
