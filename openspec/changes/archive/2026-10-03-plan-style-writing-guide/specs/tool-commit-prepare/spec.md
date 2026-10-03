# Spec Delta

## ADDED Requirements

### Requirement: Communication style field
The tool output on success SHALL include a top-level `style` object (capability `communication-style`), read fresh from `.sdlc-v2/local.toml` on each call. Reading the style SHALL NOT fail the call: a read error gives the defaults plus one warning `Failed to read style config: <cause>`. The manifest file at `manifestPath` SHALL NOT contain `style`: the commit-orchestrator subagent reads it with no conversation context, and commit messages follow the commit config, not the chat style.

| Field | Meaning |
|---|---|
| `style.audience` | reader level in effect |
| `style.writingStandard` | writing standard in effect |
| `style.tone` | tone in effect |
| `style.language` | output language |
| `style.guide` | the chat guide; same text as the session-start block |
| `style.warnings` | style warnings; `[]` when none |

#### Scenario: Default style in the output
- **WHEN** `commit_prepare` succeeds in a project with no `[style]` section
- **THEN** `style.audience` is `functional`
- **AND** `style.guide` contains `<sdlc_communication_style>`

#### Scenario: Manifest has no style
- **WHEN** `commit_prepare` succeeds and `[style] writingStandard` is `ste`
- **THEN** the tool output has `style`
- **AND** the manifest file at `manifestPath` has no `style` key
