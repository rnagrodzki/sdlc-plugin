# Spec Delta

## Purpose

Carries one developer-chosen communication style (reader level, writing standard, tone, language) to the chat and questions of every sdlc skill, and keeps it out of text that has its own templates.

## ADDED Requirements

### Requirement: Communication style settings
The system SHALL read `audience`, `writingStandard`, `tone`, `language`, and `technicalTerms` from the `[style]` section of the main worktree's `.sdlc-v2/local.toml`, with the values, defaults, and legacy rules of capability `plan-writing-style`. Setup SHALL offer these keys as the section `communication-style`, which writes `[style]`.

#### Scenario: Defaults without a style section
- **WHEN** `.sdlc-v2/local.toml` has no `[style]` section
- **THEN** the communication style is `audience: functional`, `writingStandard: plain-language`, `tone: direct`, `language: English`

#### Scenario: Setup writes the style section
- **WHEN** setup configures the `communication-style` section with `audience: executive`
- **THEN** `.sdlc-v2/local.toml` has `[style]` with `audience = "executive"`

### Requirement: Chat guide
The system SHALL build a chat guide wrapped in `<sdlc_communication_style>` with exactly these tags in this order: `<scope>`, `<reader>`, `<tone>`, `<writing_standard>`, `<questions>`. `<reader>`, `<tone>`, and `<writing_standard>` SHALL hold the same rule text as the plan writing guide. The chat guide SHALL NOT hold plan limits, visual rules, examples, or custom plan instructions.

| Tag | Content |
|---|---|
| `<scope>` | Apply to every explanation, status line, summary, and AskUserQuestion text in every sdlc skill. Do not apply to commit messages, PR bodies, review comments, or Jira text. They follow their own templates and config. |
| `<questions>` | state what is decided, why it matters, and the facts the reader needs; give each option a concrete consequence; put the recommended option first, marked "(Recommended)" |

#### Scenario: Chat guide has no plan limits
- **WHEN** the chat guide is built for the default settings
- **THEN** it contains `<scope>` and `<questions>`
- **AND** it does not contain `<limits>` or `<visual_rules>`

### Requirement: Session-start style block
The session-start hook SHALL print the communication style on every source it receives (`startup`, `clear`, `compact`), after the `Plan mode routing:` line: one settings line, one `  warning: <text>` line per style warning, then the chat guide. A missing or unreadable style section SHALL give the defaults and SHALL NOT fail the hook. The chat guide SHALL be 4096 bytes or less for every setting combination.

#### Scenario: Style block after compaction
- **WHEN** a session compacts and `[style] audience` is `executive`
- **THEN** the hook output contains a line that starts with `sdlc communication style: audience=executive`
- **AND** the output contains `<sdlc_communication_style>`

### Requirement: Skill communication style line
Every sdlc skill except plan SHALL tell the model to follow `style.guide` from its first tool call when that call returns it, and otherwise the `sdlc communication style` block in session context, for every explanation, status line, summary, and AskUserQuestion text. The line SHALL exclude commit messages, PR bodies, review comments, and Jira text.

| Skill | Source of `style.guide` |
|---|---|
| execute | `execute_state` `read` |
| ship | `ship_state` `read` |
| review | `review_prepare` |
| pr | `pr_prepare` |
| commit | `commit_prepare` |
| received-review | `received_review_prepare` |
| harden, error-report, jira, setup, deferred, verify-pipeline | session-start block |

#### Scenario: Commit message keeps its template
- **WHEN** `[style] writingStandard` is `ste` and the commit skill drafts a commit message
- **THEN** the commit message follows the commit config and recent-commit style, not the STE rules
