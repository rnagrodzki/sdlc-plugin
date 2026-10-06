# Spec Delta

## MODIFIED Requirements

### Requirement: Style settings
The system SHALL read the keys below from the user file (`$SDLC_USER_CONFIG`, else `~/.sdlc/local.toml`) merged under the main worktree's `.sdlc-v2/local.toml`: shared keys from `[style]`, plan-only keys from `[planStyle]`. It SHALL use the default when a key or the whole section is absent from both files.

- Merge: tables merge key by key, and the project file wins. A list or a scalar in the project file replaces the user value whole (capability `local-config-layering`).

| Key | Section | Values | Default | Controls |
|---|---|---|---|---|
| `audience` | `[style]` | `technical`, `functional`, `executive`, `general`, `beginner` | `functional` | explanation depth and jargon limit |
| `writingStandard` | `[style]` | `ste`, `plain-language`, `developer-docs`, `smart-brevity` | `plain-language` | sentence and word rules |
| `tone` | `[style]` | `direct`, `neutral` | `direct` | voice and banned phrases |
| `language` | `[style]` | plain text, e.g. `English` | `English` | output language |
| `technicalTerms` | `[style]` | list of strings | empty | words the strict STE checks accept; trimmed, lower-cased, blanks and non-strings dropped |
| `visualDensity` | `[planStyle]` | `high`, `balanced`, `low` | `balanced` | prose share, paragraph and list limits |
| `narrativeRules` | `[planStyle]` | list of strings | empty | extra rules added to the guide |
| `instructions` | `[planStyle]` | list of strings | empty | process instructions |

| `audience` | Prose explains |
|---|---|
| `technical` | behavior and mechanism |
| `functional` | behavior, function, and impact; mechanism only in tables, code blocks, diagrams, and contracts |
| `executive` | impact, cost, risk, and the decision needed |
| `general` | what a user sees, in everyday words |
| `beginner` | one idea and one concrete example per concept |

#### Scenario: No style sections
- **WHEN** neither `~/.sdlc/local.toml` nor `.sdlc-v2/local.toml` has a `[style]` or a `[planStyle]` section
- **THEN** the settings are `audience: functional`, `writingStandard: plain-language`, `tone: direct`, `visualDensity: balanced`, `language: English`, `technicalTerms: []`
- **AND** there are no style warnings

#### Scenario: Technical terms are cleaned
- **WHEN** `[style] technicalTerms` is `[" Logging ", 3, ""]`
- **THEN** `technicalTerms` is `["logging"]`

#### Scenario: Style from the user file
- **WHEN** `~/.sdlc/local.toml` has `[style] audience = "functional"` and `tone = "neutral"`
- **AND** `.sdlc-v2/local.toml` has `[style] audience = "technical"`
- **THEN** the audience is `technical`
- **AND** the tone is `neutral`

#### Scenario: Project list replaces the user list
- **WHEN** `~/.sdlc/local.toml` has `[planStyle] instructions = ["A", "B"]`
- **AND** `.sdlc-v2/local.toml` has `[planStyle] instructions = ["C"]`
- **THEN** `instructions` is `["C"]`
