# Spec Delta

## MODIFIED Requirements

### Requirement: Output fields
On success the tool SHALL return the top-level fields below. The tool SHALL NOT return a top-level `warnings` field.

| Field | Meaning |
|---|---|
| `next` | Next-step instruction (see "Next text") |
| `runId` | Plan run ID: the state file name without `.json`, e.g. `plan-main-20260509T140000Z` |
| `guardrailsFile` | Absolute path of `<runId>.evidence/guardrails.md` |
| `styleGuideFile` | Absolute path of `<runId>.evidence/style-guide.md`, which holds `style.writingGuide` |
| `openspec` | OpenSpec detection: `present`, `specsCount`, `activeChanges[]`, `branchMatch`, `authoritative` |
| `fromOpenspec` | Change validation result, or null when `fromOpenspec` input is empty |
| `openspecContext` | `tasks`, `tasksUpdated`, `requirements`, `requirementsError` |
| `guardrails` | Plan guardrails from config |
| `style` | `audience`, `writingStandard`, `tone`, `visualDensity`, `language`, `technicalTerms`, `narrativeRules`, `instructions`, `warnings`, `limits`, `writingGuide` |
| `tasks` | `requiredFields`, `contractShape` |
| `explorePack` | `manifestPath`, `outDir`, `scopeHintCount`, `webResearchSignal`, `error` |
| `planTemplate` | `path` of the project template override, or null |
| `githubHosting` | `detected`, `host` |
| `g17Dispatch` / `intakeAuditDispatch` | `subagentType`, `model`, `promptTemplatePath` |
| `lanes` | Five Step 3 lane dispatch entries |
| `lensReviewers` | Three Step 5 lens dispatch entries |
| `reviewLoop` | `maxRounds`: the Step 5 review-loop limit, `5` |
| `template` | Template resolution; present only when the template was resolved |
| `errors` | Soft error strings (see "Soft errors") |

#### Scenario: Top-level key set
- **WHEN** `plan_prepare({skipConfigCheck: true})` succeeds
- **THEN** the output has `openspec`, `fromOpenspec`, `openspecContext`, `guardrails`, `style`, `tasks`, `explorePack`, `planTemplate`, `githubHosting`, `g17Dispatch`, `intakeAuditDispatch`, `lanes`, `lensReviewers`, `reviewLoop`, `errors`
- **AND** it has no `warnings` field

#### Scenario: Style guide file
- **WHEN** `plan_prepare` succeeds with a run ID
- **THEN** the file at `styleGuideFile` has the same content as `style.writingGuide`

#### Scenario: Review loop limit
- **WHEN** `plan_prepare` succeeds, fresh or with `resume: true`
- **THEN** `reviewLoop.maxRounds` is 5

### Requirement: Config loading
The tool SHALL read guardrails, style, and task contract settings from the project config, and SHALL fall back to defaults when a section is absent.

| Output | Source | Default / rule |
|---|---|---|
| `guardrails` | `.sdlc-v2/config.toml` `[plan.guardrails.<id>]` or `[[plan.guardrails]]` | `[]`; each entry carries its `id` and raw keys. Named tables take `id` from the table key and are sorted by it; array entries keep their own `id` and file order |
| `style.audience` | `.sdlc-v2/local.toml` `[style]` (legacy: `[planStyle]`) | `functional`; values and fallback per capability `plan-writing-style` |
| `style.writingStandard` | `[style]` (legacy: `[planStyle]`) | `plain-language` |
| `style.tone` | `[style]` (legacy: `[planStyle]`) | `direct` |
| `style.visualDensity` | `[planStyle]` | `balanced` |
| `style.language` | `[style]` (legacy: `[planStyle]`) | `English`; trimmed |
| `style.technicalTerms` | `[style]` (legacy: `[planStyle]`) | empty; trimmed, lower-cased, non-strings and blanks dropped |
| `style.narrativeRules` | `[planStyle]` | empty; non-strings dropped |
| `style.instructions` | `[planStyle]` | empty; non-strings and blank entries dropped, the rest trimmed |
| `style.warnings` | derived | one string per invalid or legacy key; empty when none |
| `style.limits` | derived | numeric limits per capability `plan-writing-style` |
| `style.writingGuide` | derived | Markdown guide per capability `plan-writing-style` |
| `tasks.contractShape` | `[plan.tasks]` | `full` |
| `tasks.requiredFields` | `[plan.tasks]` | empty; `Complexity`, `Risk`, `Files`, `Verify`, `Depends on` are dropped |

#### Scenario: Guardrails as an array of tables
- **WHEN** `config.toml` holds two `[[plan.guardrails]]` entries with `id` `prefer-existing-helpers` then `no-new-deps`
- **THEN** `guardrails.md` lists `## prefer-existing-helpers (warning)` before `## no-new-deps (error)`

#### Scenario: Core task fields are deduplicated
- **WHEN** `[plan.tasks] requiredFields` is `["Complexity", "Risk", "Files", "Verify", "Depends on", "Owner", "Rollback"]`
- **THEN** `tasks.requiredFields` is `["Owner", "Rollback"]`

#### Scenario: Instructions are cleaned
- **WHEN** `[planStyle] instructions` is `["A", "  ", 3, " B "]`
- **THEN** `style.instructions` is `["A", "B"]`

#### Scenario: Invalid style value
- **WHEN** `[style] tone` is `friendly`
- **THEN** `style.tone` is `direct`
- **AND** `style.warnings` has one entry that names `style.tone`

### Requirement: Dispatch metadata
The tool SHALL return fixed dispatch metadata for the skill's sub-agents. Every entry uses `subagentType: general-purpose`.

| Entry | Model | Gate IDs / focus | Prompt template |
|---|---|---|---|
| `lanes[0]` `static-structural` | `haiku` | G1, G2, G3, G7, G12 | `lane-static-structural-prompt.md` |
| `lanes[1]` `content-coverage` | `sonnet` | G5, G6, G8, G9, G11, G13, G15, G16, G18, G19, G20, G21 | `lane-content-coverage-prompt.md` |
| `lanes[2]` `file-existence` | `haiku` | G4, G10 | `lane-file-existence-prompt.md` |
| `lanes[3]` `guardrail-compliance` | `sonnet` | G14, G22 | `lane-guardrail-compliance-prompt.md` |
| `lanes[4]` `dimension-coverage` | same as `g17Dispatch` | G17 | `g17-dimension-coverage-prompt.md` |
| `lensReviewers[0]` `architecture` | `sonnet` | Buildability, Task descriptions, Decision documentation, Dependency accuracy | `lens-architecture-prompt.md` |
| `lensReviewers[1]` `requirements` | `sonnet` | Requirements coverage, Metadata completeness, Plan completeness, OpenSpec G16, Exploration provenance, Best-practice traceability | `lens-requirements-prompt.md` |
| `lensReviewers[2]` `risk` | `sonnet` | File paths, Verification strategy, Scope discipline, Guardrail compliance | `lens-risk-prompt.md` |
| `g17Dispatch` | `sonnet` | — | `g17-dimension-coverage-prompt.md` |
| `intakeAuditDispatch` | `sonnet` | — | `intake-verify-prompt.md` |

- `promptTemplatePath` is the file in `$CLAUDE_PLUGIN_ROOT/skills/plan/` when present.
- Otherwise it is the highest-versioned match under `~/.claude/plugins` inside a `/plan/` path.
- It is null when neither location has the file.

#### Scenario: Lane order and G17 mirror
- **WHEN** `plan_prepare` succeeds
- **THEN** `lanes` has 5 entries named `static-structural`, `content-coverage`, `file-existence`, `guardrail-compliance`, `dimension-coverage`
- **AND** `lanes[4]` has the same `subagentType` and `model` as `g17Dispatch` and `gateIds` `["G17"]`

#### Scenario: Style gate on the guardrail lane
- **WHEN** `plan_prepare` succeeds
- **THEN** `lanes[3].gateIds` is `["G14", "G22"]`
