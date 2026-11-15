## ADDED Requirements

### Requirement: Copilot overview in the decide entry
The skill SHALL record the await-remote-review verdict with the `decide` text `verdict: <ext.verdict>; overview: <ext.overview, or none when absent or empty>`.

#### Scenario: Clean Copilot review
- **WHEN** the poll returns `done` with `ext.verdict: approved-clean` and `ext.overview: approval-recommended`
- **THEN** the skill records `verdict: approved-clean; overview: approval-recommended` and proceeds

#### Scenario: No overview
- **WHEN** the poll returns `done` with `ext.verdict: actionable` and `ext.overview: ""`
- **THEN** the skill records `verdict: actionable; overview: none`

### Requirement: Copilot closer-look note
When the poll returns `ext.overview: needs-closer-look`, the skill SHALL print `Copilot asks for a closer human look (🔵).` before the received-review dispatch. It SHALL NOT add that line to the dispatch prompt: received-review reads `overview` from `received_review_verify`. The verdict routes do not change.

#### Scenario: Closer look
- **WHEN** the await-remote-review poll returns `done` with `ext.verdict: actionable` and `ext.overview: needs-closer-look`
- **THEN** the skill prints `Copilot asks for a closer human look (🔵).`
- **AND** dispatches `received-review` with `--pr <N>` and without that line in the prompt
