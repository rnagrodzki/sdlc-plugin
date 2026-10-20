# Spec Delta

## MODIFIED Requirements

### Requirement: Copilot mirror for a new review dimension
After a successful `add` on `review-dimensions`, the skill SHALL call `dimensions_render_instructions` with `file: <targetFile>`, `commonFile: <CONTENT_ROOT>/.sdlc-v2/review-dimensions/_common.md` and `projectRoot: <CONTENT_ROOT>`, and SHALL halt the proposal loop when it fails, except when the tool refuses to replace a mirror that has hand-written content.

- `strengthen` and `consolidate` actions do not regenerate a mirror.
- On failure the skill prints `Dimension written to <targetFile> but the Copilot mirror could not be created (<error>) — resolve manually before continuing.`
- On refusal (the error contains `has hand-written content`) the skill prints `Copilot mirror skipped: hand-written content in <path>` and continues with the next proposal.
- Under `--auto`, a refused mirror appends `; Copilot mirror skipped: hand-written content in <path>` to the `Auto-accepted` line of its proposal.
- The skill never passes `overwrite`. A user adopts a refused mirror by moving its hand-written rows into the dimension file, then calling the tool with `overwrite: true`.
- On success it prints `Mirrored review dimension → <path>`.

#### Scenario: Mirror generation fails
- **WHEN** `dimensions_render_instructions` returns an error that does not contain `has hand-written content`
- **THEN** no further proposal is processed
- **AND** under `--auto` the remaining proposals are listed under `Not processed`

#### Scenario: Mirror refused
- **WHEN** `dimensions_render_instructions` returns an error containing `has hand-written content`
- **THEN** the skill prints `Copilot mirror skipped: hand-written content in <path>`
- **AND** the skill processes the next proposal
- **AND** the skill does not pass `overwrite`

#### Scenario: Mirror refused under auto mode
- **WHEN** `--auto` is set and the tool refuses the mirror of an accepted proposal
- **THEN** the `Auto-accepted` line of that proposal ends with `; Copilot mirror skipped: hand-written content in <path>`
