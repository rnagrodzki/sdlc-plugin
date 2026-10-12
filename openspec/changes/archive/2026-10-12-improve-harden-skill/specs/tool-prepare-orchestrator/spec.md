# Spec Delta

## MODIFIED Requirements

### Requirement: harden pre-flight validation
In `harden` mode the tool SHALL validate the existing plan guardrails, execute guardrails and review dimensions in the active worktree before building the manifest, SHALL write each finding to the manifest key `preflightFindings`, and SHALL stop only on a read or parse error.

- Each finding is `{surface, id, file, severity, message, fix, bytesOver}`. `bytesOver` is set only for an oversize guardrail description.
- `preflightFindings` is `[]` when nothing is found, never `null`.
- The output carries `preflightFindingCount`. When it is above 0, `next` adds `<N> pre-flight findings: the harden orchestrator drafts repair proposals for them first.`
- A `config.toml` that cannot be read or parsed, or a `review-dimensions` directory that cannot be read, returns an InfraError and writes no manifest.
- No guardrail config and no `review-dimensions` directory pass pre-flight.

#### Scenario: Invalid existing guardrail
- **WHEN** the active worktree `.sdlc-v2/config.toml` has a plan guardrail with a 1474-byte description
- **THEN** the tool returns ok and writes the manifest
- **AND** `preflightFindings` has one entry with `bytesOver` 450
- **AND** `preflightFindingCount` is 1

#### Scenario: Pre-flight reads the active worktree
- **WHEN** the main worktree has an invalid guardrail but the active worktree does not
- **THEN** `preflightFindingCount` is 0

#### Scenario: Config does not parse
- **WHEN** the active worktree `.sdlc-v2/config.toml` is not valid TOML
- **THEN** the tool returns an InfraError
- **AND** no `sdlc-harden-*` manifest is left behind

## ADDED Requirements

### Requirement: harden dead-reference scan
In `harden` mode the tool SHALL list each path, MCP tool, tool action, skill flag and skill step named in a rule that does not exist, in `surfaces.deadReferences`. A check that cannot run SHALL add one note to `surfaces.deadReferenceSkips` and SHALL NOT fail the call.

- Both lists are `[]` when empty, never `null`. The list keeps 200 entries. `deadReferencesTruncated` holds the rest count.

#### Scenario: Removed tool name
- **WHEN** a review dimension names `old_tool(` and no tool `old_tool` is registered
- **THEN** `surfaces.deadReferences` has one entry with kind `tool` and token `old_tool`

#### Scenario: Placeholder path
- **WHEN** a rule names the path `<dir>/file.go`
- **THEN** `surfaces.deadReferences` has no entry for it

#### Scenario: git fails
- **WHEN** `git ls-files` fails
- **THEN** `surfaces.deadReferenceSkips` has a note that starts `path check skipped: git ls-files failed:`
- **AND** the call returns ok

### Requirement: error_report similar issues
In `error_report` mode the tool SHALL run `gh issue list` once with labels `tooling-error` and the skill, for open issues, limit 10. It SHALL return in `similarIssues` the issues whose title starts with `[<skill>]`. A `gh` failure SHALL NOT fail the call.

#### Scenario: One match
- **WHEN** open issues `[harden] pre-flight stop` and `[plan] timeout` exist and the skill is `harden`
- **THEN** `similarIssues` holds only `[harden] pre-flight stop`

#### Scenario: gh not installed
- **WHEN** `gh` is not found
- **THEN** `similarIssues` is `[]`
- **AND** `similarIssuesNote` is `duplicate search skipped: gh not found`
