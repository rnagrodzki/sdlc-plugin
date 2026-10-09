# Spec Delta

## MODIFIED Requirements

### Requirement: Dispatch metadata
The tool SHALL return fixed dispatch metadata for the skill's sub-agents. Every entry uses `subagentType: general-purpose`.

| Entry | Model | Gate IDs / focus | Prompt template |
|---|---|---|---|
| `lanes[0]` `static-structural` | `haiku` | G1, G2, G3, G7, G12 | `lane-static-structural-prompt.md` |
| `lanes[1]` `content-coverage` | `sonnet` | G5, G6, G8, G9, G11, G13, G15, G16, G18, G19, G20, G21 | `lane-content-coverage-prompt.md` |
| `lanes[2]` `file-existence` | `haiku` | G4, G10 | `lane-file-existence-prompt.md` |
| `lanes[3]` `guardrail-compliance` | `sonnet` | G14 | `lane-guardrail-compliance-prompt.md` |
| `lanes[4]` `dimension-coverage` | same as `g17Dispatch` | G17 | `g17-dimension-coverage-prompt.md` |
| `lanes[5]` `style-compliance` | `sonnet` | G22 | `lane-style-compliance-prompt.md` |
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
- **THEN** `lanes` has 6 entries named `static-structural`, `content-coverage`, `file-existence`, `guardrail-compliance`, `dimension-coverage`, `style-compliance`
- **AND** `lanes[4]` has the same `subagentType` and `model` as `g17Dispatch` and `gateIds` `["G17"]`

#### Scenario: Style gate on the guardrail lane
- **WHEN** `plan_prepare` succeeds
- **THEN** `lanes[3].gateIds` is `["G14"]`, so the guardrail lane no longer holds the style gate
- **AND** `lanes[5].gateIds` is `["G22"]`
