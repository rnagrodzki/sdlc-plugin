# Spec Delta

## ADDED Requirements

### Requirement: Unauthored OpenSpec plan is refused
`ship_prepare` SHALL refuse a plan that has an `**OpenSpec-Create:**` line and no `**OpenSpec-Staging:**` or `**OpenSpec-Saved:**` line, or a Create or Staging line, no Saved line, and an OpenSpec Appendix that starts with `[TBD`. The refusal is in `errors`, names the plan file and the change, holds the fix text, and no state file is written. Text in a code fence is ignored.

#### Scenario: Create line only
- **WHEN** the plan has `**OpenSpec-Create:** cimd-document-redirects` and no Staging or Saved line
- **THEN** `errors` holds `plan <plan-file> names OpenSpec change "cimd-document-redirects" (**OpenSpec-Create:**), but its artifacts are not authored`
- **AND** the message holds `Run /sdlc:plan on the plan again`
- **AND** no ship state file is written

#### Scenario: Saved plan passes
- **WHEN** the plan has an `**OpenSpec-Saved:**` line
- **THEN** `errors` holds no OpenSpec header message

#### Scenario: Header in a code fence
- **WHEN** the only `**OpenSpec-Create:**` line is inside a code fence
- **THEN** `errors` holds no OpenSpec header message

### Requirement: Shared capability warning and base commit
`ship_prepare` SHALL add one warning for each capability spec that the named change and another active or staged change both edit, and SHALL store HEAD as `openspecBaseHead` in the ship state. Neither runs on a dry run. A failed `git rev-parse HEAD` adds a warning and stores no key.

#### Scenario: Shared spec
- **WHEN** the named change `a` and the active change `b` both have `specs/auth/`
- **THEN** `warnings` holds `OpenSpec change "a" and active change "b" both edit specs/auth/. Merge order decides which delta must be rebased.`

#### Scenario: rev-parse fails
- **WHEN** `git rev-parse HEAD` fails
- **THEN** `warnings` holds a line that starts with `openspecBaseHead not stored: git rev-parse HEAD:`
- **AND** the ship state has no `openspecBaseHead` key
