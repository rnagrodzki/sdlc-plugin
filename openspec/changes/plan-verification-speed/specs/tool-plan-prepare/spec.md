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
| `gateChecks` | Two Step 3 gate checks that start no sub-agent: `go-gates` and `inline-checks` |
| `reviewLoop` | `maxRounds`: the Step 5 review-loop limit, `5` |
| `template` | Template resolution; present only when the template was resolved |
| `errors` | Soft error strings (see "Soft errors") |

#### Scenario: Top-level key set
- **WHEN** `plan_prepare({skipConfigCheck: true})` succeeds
- **THEN** the output has `openspec`, `fromOpenspec`, `openspecContext`, `guardrails`, `style`, `tasks`, `explorePack`, `planTemplate`, `githubHosting`, `g17Dispatch`, `intakeAuditDispatch`, `lanes`, `lensReviewers`, `gateChecks`, `reviewLoop`, `errors`
- **AND** it has no `warnings` field

#### Scenario: Style guide file
- **WHEN** `plan_prepare` succeeds with a run ID
- **THEN** the file at `styleGuideFile` has the same content as `style.writingGuide`

#### Scenario: Review loop limit
- **WHEN** `plan_prepare` succeeds, fresh or with `resume: true`
- **THEN** `reviewLoop.maxRounds` is 5

### Requirement: Dispatch metadata
The tool SHALL return fixed dispatch metadata for the skill's sub-agents and gate checks. Every lane and lens entry uses `subagentType: general-purpose`.

| Entry | Model | Gate IDs / focus | Prompt template |
|---|---|---|---|
| `lanes[0]` `static-structural` | `haiku` | G1, G2, G7 | `lane-static-structural-prompt.md` |
| `lanes[1]` `content-coverage` | `sonnet` | G11, G16, G18, G19, G20, G21 | `lane-content-coverage-prompt.md` |
| `lanes[2]` `guardrail-compliance` | `sonnet` | G14 | `lane-guardrail-compliance-prompt.md` |
| `lanes[3]` `dimension-coverage` | same as `g17Dispatch` | G17 | `g17-dimension-coverage-prompt.md` |
| `lanes[4]` `style-compliance` | `sonnet` | G22 | `lane-style-compliance-prompt.md` |
| `lensReviewers[0]` `architecture` | `sonnet` | Buildability, Task descriptions, Decision documentation, Dependency accuracy | `lens-architecture-prompt.md` |
| `lensReviewers[1]` `requirements` | `sonnet` | Requirements coverage, Metadata completeness, Plan completeness, OpenSpec G16, Exploration provenance, Best-practice traceability | `lens-requirements-prompt.md` |
| `lensReviewers[2]` `risk` | `sonnet` | File paths, Verification strategy, Scope discipline, Guardrail compliance | `lens-risk-prompt.md` |
| `g17Dispatch` | `sonnet` | — | `g17-dimension-coverage-prompt.md` |
| `intakeAuditDispatch` | `sonnet` | — | `intake-verify-prompt.md` |

| Gate check | `kind` | `gateIds` |
|---|---|---|
| `gateChecks[0]` `go-gates` | `validate` | G3, G4, G8, G9, G10, G12 |
| `gateChecks[1]` `inline-checks` | `inline` | G5, G6, G13, G15 |

- `promptTemplatePath` is the file in `$CLAUDE_PLUGIN_ROOT/skills/plan/` when present.
- Otherwise it is the highest-versioned match under `~/.claude/plugins` inside a `/plan/` path.
- It is null when neither location has the file.
- The lanes and the gate checks cover each gate from G1 to G22 exactly one time.

#### Scenario: Lane order and G17 mirror
- **WHEN** `plan_prepare` succeeds
- **THEN** `lanes` has 5 entries named `static-structural`, `content-coverage`, `guardrail-compliance`, `dimension-coverage`, `style-compliance`
- **AND** `lanes[3]` has the same `subagentType` and `model` as `g17Dispatch` and `gateIds` `["G17"]`

#### Scenario: Style gate on the guardrail lane
- **WHEN** `plan_prepare` succeeds
- **THEN** `lanes[2].gateIds` is `["G14"]`, so the guardrail lane does not hold the style gate
- **AND** `lanes[4].gateIds` is `["G22"]`

#### Scenario: Gate checks
- **WHEN** `plan_prepare` succeeds
- **THEN** `gateChecks` has 2 entries named `go-gates` and `inline-checks`
- **AND** `go-gates` has `kind: validate` and `gateIds` `["G3","G4","G8","G9","G10","G12"]`

#### Scenario: One owner for each gate
- **WHEN** the `gateIds` of every `lanes[]` entry and every `gateChecks[]` entry are joined
- **THEN** the list holds each gate from G1 to G22 exactly one time
