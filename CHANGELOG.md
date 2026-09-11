# Changelog

## [0.0.3] - 2026-09-11

### RC 1

- Add full reference documentation for all 11 remaining SDLC skills (docs/skills/), linked from getting-started.md
- Remove unused /deliver skill and its guard test
- Fix documented --rebase default and --draft behavior found during code review
- Fix a gh prerequisite gap and a scope-option count error in docs found during code review
- Sync pluginVersion constant to plugin.json's 0.0.2, fixing a version-drift test
- Extend PR preflight to always compute the full commit list since branch diverged from default branch; add AI-attribution guards to raw gh CLI posting call sites

### RC 2

- Ship flag merging now enforces an "always-rc" pre-release policy across the release pipeline (e5750c3)
- Review-round fixes applied to the always-rc enforcement (a4f313c)

### RC 3

- Repair v4 config import to run the full v4→v5→v6 migration chain and strip stale $schema (36b69cc)
- Add CLI execution evidence logging, changelog aggregation, and received-review verification tooling to the release pipeline (36b69cc)
- Address 19 code-review findings (6 high severity) from a received-review pass: schema/error hardening, wrapped I/O errors, expanded test coverage (8848b3b)
- Resync the release-on-main.cjs CI mirror with its source copy (a459208)
- Register received_review_verify in the golden MCP tool-surface test (b5b6e40)
- Enforce always-rc pre-release policy across the release pipeline (5729c69)
- Add full skill documentation, remove /deliver skill (26d5f9c)

### RC 4

- Plan-drift detection at wave-start: execute halts on stale/mismatched plan hash instead of silently continuing
- Push default-branch hard gate (KD-1), with config-driven feature-branch auto-approve override
- Execution-report and drift-backed issue-draft actions wired into ship's deferred-followups step
- Run-state storage migrated from `.sdlc-v2/execution/` to `.sdlc-v2/runs/`, with legacy-directory fallback and an explicit `migrate layout` action
- Fixed a data-loss bug where hardcoded legacy-path literals caused the ledger reaper to delete live run directories after the runs/ migration
- Hardened MCP tool contracts (missing Suggestion/Next/enum schema fields), fixed a silent-overwrite risk in migration's stat-error handling, and fixed nil-slice JSON serialization on report/error outputs
- v4 config import repair and always-rc release-policy enforcement across the release pipeline (#11, #12)
- Full skill documentation added, `/deliver` skill removed (#10)

### RC 5

- Fixed promote-release's tag-dispatch ref/fetch bug, hardened learnings_log's --from-learnings bulk-triage mode, and fixed ship's execute-dispatch pipelineAuto forwarding
- Added schema constraints and a Next field to learnings_log's remove action, and fixed execute_state's pipelineAuto cross-read (review-step follow-up)
- Added test coverage for the remove action and documented the --from-learnings triage mode
- Added drift detection, a runs/ state layout, and release gates to execute/ship
- Repaired v4 config import and hardened the release pipeline during migrate/ship
- Enforced the always-rc policy consistently across the release pipeline
- Added full skill documentation and removed the unused /deliver skill

## [0.0.2] - 2026-09-10

## Added
- Next-step guidance to ship prepare and verify side-effect outputs

## Changed
- Moved release-intent ownership from version step into pr step
- Consolidated version diagnostics and schema handling into pr step

## Removed
- Version MCP tools and CLI registration
- Standalone version skill (functionality absorbed into pr step)

## Fixed
- MCP stdio startup: migrated off broken mark3labs/mcp-go to the official SDK
- GoReleaser build dispatch timing after release-on-main tags

## [0.0.1] - 2026-09-10

- Added a release-intent gate to `pr_apply`, requiring an explicit `releaseLevel` or an acknowledged `skipReleaseCheck`, keyed by `releaseSource` (`user`/`config`/`pipeline`) so unattended runs can't claim a human decided.
- Standardized a `Next` field across pr and commit tool outputs.
- Added `jsonschema_description` and `enum` tags across MCP tool structs for stricter, self-documenting contracts.
- Added MCP contract review dimensions, guardrails, and developer helper agents for auditing tool contracts.

## [0.0.1-rc3] - 2026-09-09

Fixed error report target repository configuration

## [0.0.1-rc2] - 2026-09-09

### Added
- RC release note aggregation and CHANGELOG collapse when promoting release candidates to final release
- Automatic deduplication of identical notes across RC versions

### Changed
- Improved error handling and logging in release scripts

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.0.0]

- Initial Go port of the sdlc plugin.
