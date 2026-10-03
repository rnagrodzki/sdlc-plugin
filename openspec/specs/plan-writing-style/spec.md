# plan-writing-style Specification

## Purpose
Turns a developer's structured plan style settings into one writing guide that the plan author and the plan reviewers follow, into numeric limits and strict STE checks that the plan validator applies, keeps plan diagrams readable, and returns the custom plan instructions to context at every step.

## Requirements

### Requirement: Style settings
The system SHALL read the keys below from the main worktree's `.sdlc-v2/local.toml`: shared keys from `[style]`, plan-only keys from `[planStyle]`. It SHALL use the default when a key or the whole section is absent.

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
- **WHEN** `.sdlc-v2/local.toml` has no `[style]` and no `[planStyle]` section
- **THEN** the settings are `audience: functional`, `writingStandard: plain-language`, `tone: direct`, `visualDensity: balanced`, `language: English`, `technicalTerms: []`
- **AND** there are no style warnings

#### Scenario: Technical terms are cleaned
- **WHEN** `[style] technicalTerms` is `[" Logging ", 3, ""]`
- **THEN** `technicalTerms` is `["logging"]`

### Requirement: Invalid and legacy values
The system SHALL replace an enum value that is not in its list with the key's default, and SHALL add one warning for each replaced key. A `verbosity` key SHALL be ignored with one warning. A shared key found only in `[planStyle]` SHALL be used, with one warning. A plan-only key in `[style]` SHALL be ignored, with one warning. When `[style]` and `[planStyle]` both set a shared key, the `[style]` value SHALL win with no warning.

#### Scenario: Retired audience value
- **WHEN** `[style] audience` is `mixed`
- **THEN** the audience is `functional`
- **AND** the warnings contain `style.audience "mixed" is not valid (technical | functional | executive | general | beginner); using "functional"`

#### Scenario: Shared key in the plan section
- **WHEN** `[style]` has no `audience` and `[planStyle] audience` is `general`
- **THEN** the audience is `general`
- **AND** the warnings contain `planStyle.audience moved to [style]; move it there (the value still works)`

#### Scenario: Plan-only key in the shared section
- **WHEN** `[style] visualDensity` is `high`
- **THEN** `visualDensity` is `balanced`
- **AND** the warnings contain `style.visualDensity belongs in [planStyle]`

#### Scenario: Legacy verbosity key
- **WHEN** `[planStyle] verbosity` is `terse`
- **THEN** the warnings contain `planStyle.verbosity is no longer used; set visualDensity (high | balanced | low) instead`

### Requirement: Writing guide content
The system SHALL build one Markdown writing guide that holds these XML-tagged sections in this order: `<reader>`, `<tone>`, `<writing_standard>`, `<layout>`, `<visual_rules>`, `<limits>`, `<extra_rules>`, `<custom_instructions>`, `<example>`.

- `<layout>` always tells the writer to lead each section with its conclusion (BLUF), to use prose only for what a change does, why, and its effect, and to put mechanism (names, types, code, paths) only in tables, code blocks, diagrams, and contracts.
- `<visual_rules>` always quotes the two Mermaid classDefs from "Diagram contrast" and tells the writer to copy them exactly and not to use pastel fills.
- `<extra_rules>` holds each `narrativeRules` entry verbatim, or `none`.
- `<custom_instructions>` holds the `instructions` as numbered lines (`1. <text>`), or `none configured`.
- `<example>` holds one before/after pair for the selected `writingStandard`.
- With `writingStandard: ste`, `<writing_standard>` holds the 20 ASD-STE100 rules: words (simple words, one meaning per word, technical names, noun clusters of 3 words or fewer, avoid list), verbs (simple tenses only, no perfect tenses, no -ing forms, past participle only as an adjective, active voice, no phrasal verbs), sentences (one instruction per sentence, imperative with condition first, 20 words per instruction, 25 words per description, articles kept, clear pronouns, cause and effect as two sentences), paragraphs (one topic, 6 sentences or fewer, vertical lists, warnings start with the command), and punctuation and style (no contractions, semicolons, idioms, or figurative words).
- The avoid list is a plugin approximation of common non-STE words, not the ASD-STE100 dictionary.

#### Scenario: Default guide keeps the R62 rules
- **WHEN** the settings are the defaults
- **THEN** the guide contains `One idea in each sentence` and `Explain each uncommon technical term the first time you use it`

#### Scenario: STE guide carries the full rule set
- **WHEN** `writingStandard` is `ste`
- **THEN** the `<writing_standard>` section contains 20 numbered rules, `20 words`, `25 words`, and `Do not use "-ing" verb forms`

#### Scenario: Extra rules are kept verbatim
- **WHEN** `narrativeRules` is `["Name the owner of each risk."]`
- **THEN** the `<extra_rules>` section contains `Name the owner of each risk.`

#### Scenario: Custom instructions in the guide
- **WHEN** `instructions` is `["A", "B"]`
- **THEN** the `<custom_instructions>` section contains `1. A` and `2. B`

### Requirement: Numeric limits
The system SHALL derive these limits from the settings.

| `visualDensity` | `maxProseShare` | `maxParagraphSentences` | `maxListItems` |
|---|---|---|---|
| `high` | 0.30 | 3 | 7 |
| `balanced` | 0.50 | 5 | 9 |
| `low` | 0.75 | 6 | 12 |

| `writingStandard` | `maxSentenceWords` | `maxInstructionWords` | `ste` | `maxParagraphSentences` |
|---|---|---|---|---|
| `ste` | 25 | 20 | true | the density value, at most 6 |
| `plain-language` | 25 | 0 (off) | false | the density value |
| `developer-docs` | 30 | 0 (off) | false | the density value |
| `smart-brevity` | 20 | 0 (off) | false | the density value |

| `audience` | `maxJargonShare` |
|---|---|
| `technical` | 1.00 (no limit) |
| `functional` | 0.15 |
| `executive` | 0.05 |
| `general` | 0.00 |
| `beginner` | 0.00 |

- `tone: direct` sets `bannedPhrases` to: `great question`, `great idea`, `excellent question`, `excellent point`, `you might want to consider`, `you may want to consider`, `it might be worth`, `it is worth noting`, `it's worth noting`, `i hope this helps`, `happy to help`, `feel free to`, `needless to say`, `as you can see`, `to be honest`. `tone: neutral` sets an empty list.
- `maxSentenceWords` applies only when `language` is `English` or `en`, compared without letter case.

#### Scenario: High density beginner limits
- **WHEN** `visualDensity` is `high` and `audience` is `beginner`
- **THEN** `maxProseShare` is 0.30, `maxParagraphSentences` is 3, `maxListItems` is 7, and `maxJargonShare` is 0.00

#### Scenario: Default audience limits jargon
- **WHEN** the settings are the defaults
- **THEN** `maxJargonShare` is 0.15

#### Scenario: STE caps paragraph length
- **WHEN** `writingStandard` is `ste` and `visualDensity` is `low`
- **THEN** `maxParagraphSentences` is 6 and `maxInstructionWords` is 20

#### Scenario: Non-English output
- **WHEN** `language` is `Polish`
- **THEN** the sentence-length limit is off

### Requirement: Strict STE checks
When `writingStandard` is `ste`, the system SHALL check plan prose against the deterministic STE rules below and SHALL report each break as one hit with a rule id, the matched text, and the plan line. Words in inline code, fenced blocks, table rows, and `technicalTerms` SHALL NOT produce a hit.

| Rule id | Fails on |
|---|---|
| `contraction` | a word in the closed contraction list, such as `don't` or `it's`; any other `'s` word is a possessive and is not a hit |
| `semicolon` | `;` in prose |
| `ing-form` | a word ending in `-ing` used as a verb: right after a preposition (`before`, `after`, `by`, `for`, `when`, `while`, `without`, `on`, `in`, `of`) or after a form of `be`; an `-ing` noun after an article or adjective (`the warning`, `each setting`) is not a hit |
| `perfect-tense` | `has`, `have`, or `had` followed by a past participle |
| `phrasal-verb` | a listed phrasal verb such as `set up` or `find out` |
| `avoid-word` | a word from the avoid list such as `utilize` |
| `instruction-length` | an instruction sentence of more than 20 words |
| `description-length` | a description sentence of more than 25 words |
| `paragraph-length` | a paragraph of more than 6 sentences |
| `passive-instruction` | an instruction sentence in the passive voice |

- Rules that need meaning (noun clusters, one meaning per word, articles, condition first, warnings) are judged by the G22 lane, not by these checks.

#### Scenario: Ing form in prose
- **WHEN** `writingStandard` is `ste` and a prose line says `Before starting the tool, read the guide.`
- **THEN** the hits contain rule `ing-form` with text `starting`

#### Scenario: Ing noun is not a hit
- **WHEN** `writingStandard` is `ste` and a prose line says `The warning names each setting.`
- **THEN** there are no `ing-form` hits

#### Scenario: Technical term is exempt
- **WHEN** `technicalTerms` is `["logging"]` and a prose line contains `logging`
- **THEN** no `ing-form` hit names `logging`

#### Scenario: Other standards skip STE checks
- **WHEN** `writingStandard` is `plain-language` and a prose line contains `don't`
- **THEN** there are no STE hits

### Requirement: Diagram contrast
The system SHALL define two Mermaid classes for new and changed nodes, and SHALL report each `classDef` or `style` line that is hard to read: inside a mermaid fence when it checks a whole file, and on any line that starts with `classDef` or `style` when it checks an edit's new text.

| Class | Exact line |
|---|---|
| new | `classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px` |
| changed | `classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px` |

| Line | Result |
|---|---|
| sets `fill:` and no `color:` | hit, reason `no text color` |
| hex `fill:` and hex `color:` with WCAG contrast below 4.5:1 | hit, reason `contrast <r>:1 < 4.5:1` (one decimal) |
| non-hex colors with `color:` set | no hit |

#### Scenario: Pastel fill without text color
- **WHEN** a mermaid block has `classDef new fill:#d4f7d4,stroke:#2a7a2a`
- **THEN** that line is a hit with reason `no text color`

#### Scenario: Light gold with white text
- **WHEN** a mermaid block has `classDef changed fill:#b8860b,color:#ffffff`
- **THEN** that line is a hit with reason `contrast 3.3:1 < 4.5:1`

#### Scenario: Shipped classes pass
- **WHEN** a mermaid block has the two exact lines from the table
- **THEN** there are no hits

### Requirement: Custom instructions returned to context
The system SHALL return the full text of the `[planStyle] instructions` in these places, read fresh from `local.toml` on each call:

| Place | Form |
|---|---|
| writing guide `<custom_instructions>` | numbered lines |
| `plan_mark` checkpoint `next` | `Custom plan instructions (follow them in this step):` + numbered lines |
| `validate` `plan_style` `styleReport.instructions` | list of strings |
| session-start hook after a compaction, with an active plan run | `Custom plan instructions (follow them in every step):` + numbered lines, or `Custom plan instructions: none configured.` |

- The hook keeps the `Active plan (post-compact):` line first. A missing or unreadable `[style]` or `[planStyle]` section gives the default style and never fails the hook.

#### Scenario: Instructions after compaction
- **WHEN** a session compacts with an active plan run and `instructions` is `["A"]`
- **THEN** the hook output contains `Custom plan instructions (follow them in every step):` and `1. A` after the `Active plan (post-compact):` line
