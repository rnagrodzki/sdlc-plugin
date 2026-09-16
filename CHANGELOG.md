# Changelog

## [0.1.4] - 2026-09-16

- Added a new `task-redispatch` action for reclaiming and reassigning stalled or timed-out tasks
- Added server-owned task state tracking and a unified stall classifier, replacing the old client-side `ClassifyStall`/`StallCause`/`NudgedAt` logic
- Fixed schema issues (new `in_progress` status enum, stale path references) and typed the `task-redispatch` return value
- Refreshed and swept supporting documentation for the new stall model, with added skillcheck coverage
- Replaced the execute skill's in-context, 12-stage wave-supervision loop with a single server-driven `wave-await` MCP action
- Seeded server-side wave state at dispatch time and surfaced `resumeFrom` claims in the retry worker's fact sheet so redispatched tasks retain prior progress context

## [0.1.3] - 2026-09-16

- Add a golden regression test for tool annotations, plus documentation
- Add warnings for unreadable OpenSpec plan files
- Relocate the openspec tasks.md ref-stamp write out of plan_prepare into execute_state's init handler, keeping plan_prepare honestly read-only
- Require MCP tool Annotations (title/readOnly/destructive/idempotent/openWorld) on every mcpserver.Register call, and annotate all 31 existing tool registrations

## [0.1.2] - 2026-09-15

- Add CI push-with-secret authentication mode for protected branches
- Add structured outputs across ship tooling: build commit hash, CI drift detection, learnings stats
- Address review findings and strengthen guardrails across the pipeline
- Document waves 1-5 (resolution trace, CI drift, worktree anchoring, structured harden/review output, learnings stats, skill recommendations, state-first step, push-with-secret CI auth)
- Extract config templates into browsable TOML files; add skill-doc-drift review dimension
- Precompute ship report data and expose a skill-recommendation surface for harden
- Surface version info in ship_prepare; anchor git worktrees to the bare repo; add structured review fields; enforce state-first SKILL.md ordering

## [0.1.1] - 2026-09-14

- Adds "default-rc" preReleasePolicy so /ship --bump patch produces a final release instead of an RC by default
- Cleans up stale JSON config during setup
- Follow-up commit fixing 15 code-review findings: TasksJSON/log-cli doc-schema mismatches, id-comparison consistency, DomainError Suggestion population, dedup fixes, new test coverage
- Promote-release now uses a patch/minor/major dropdown instead of free-text version input
- Ship executeDispatchArgs --quality interpolation fix
- Supports cross-level RC promotion (e.g. promoting a patch-level RC as a minor or major release)
- Target version is auto-calculated from the latest stable tag
- Wave-start tasksJson validation hardening and phantom-success detection in execute_state.go
- execute SKILL.md batch-phantom-defense documentation

## [0.1.0] - 2026-09-14

### RC 1

- Fix `pr.go`'s NeedsPush handling to fail safe when push state can't be determined, with added test coverage
- Fix missingWorkers polling handling and ledger path resolution in the execute/plan pipeline; enrich plan state and add an exit-code-8 regression test
- Add PostToolUse hooks that automatically record CLI/MCP execution telemetry
- Close out test-coverage and documentation drift found during a received-review pass
- Embed plugin.json's version at compile time via go:embed as the single source of truth, closing the recurring post-release version-drift test failure

### RC 2

- Fixed execute-skill bugs tracked as GitHub issues #24-#29, #17, #18 (dependency-ref parsing, flag passthrough, shell-quoting guidance, wave-start task JSON validation, fact-sheet field naming, real run ID threading through task-context/report-back)
- Expanded task-context worker briefing to include wave context, quality gates, sibling-task awareness, and execution rules
- Added structured progress milestones with server-side stallCause classification
- Added a deterministic per-task stall/nudge response protocol
- Added state-transition test coverage
- Addressed 12 of 13 findings from code review; 1 finding (intentional double state.Write crash-safety checkpoint) kept with documented reasoning

### RC 3

- Promote-release now uses a patch/minor/major dropdown instead of free-text version input
- Target version is auto-calculated from the latest stable tag
- Supports cross-level RC promotion (e.g. promoting a patch-level RC as a minor or major release)
- Adds "default-rc" preReleasePolicy so /ship --bump patch produces a final release instead of an RC by default
- Cleans up stale JSON config during setup

## [0.0.4] - 2026-09-13

### RC 1

- Pin GoReleaser builds to the dispatched tag (env var + config-level pre-release-suffix guard) so binaries always build against the correct release, not a co-located RC tag
- Bridge release promotion to dispatch the release build workflow at the final tag after creating the GitHub release, with retry and graceful degradation on transient failures
- Harden release-dispatch tag resolution with sorted, RC-filtered tag selection
- Bold the ship pipeline's always-rc policy-override notices and close the gap where the interactive "change level" path skipped the same enforcement
- Add duplicate-issue search to the error-report skill so matching issues get a comment instead of a duplicate filing
- Sync the `pluginVersion` fallback const with `plugin.json` (0.0.2 -> 0.0.3), fixing a pre-existing failing test

### RC 2

- Ship pipeline execution reports now include CLI evidence (time-windowed and capped), step timings, guardrail decisions, and links to related learnings entries
- Guardrail decide-action JSON keys renamed for consistency (decideId/decideDecision/decideReason); execute-state schema extended with guardrailDecisions and pendingIssueDrafts
- Ship skill rendering order updated so the execution report appears before the history-record step
- Release pipeline hardened with error-report deduplication to prevent duplicate tooling-error issues (#15), building on the v0.0.3 promotion
- Fixed a wave-2 regression in guardrail/learnings tracking and hardened error handling throughout

### RC 3

- Promoted version to v0.0.3
- Hardened release pipeline; added error-report dedup (#15)
- Added CLI evidence, timings, and learnings to execute report (#16)
- Hardened setup flow
- Fixed harden skill failing to create GitHub issues

### RC 4

- Migrate plugin config format from JSON to TOML (config.json/local.json → config.toml/local.toml)
- Add TOML dependency and read/write helpers in fsx
- Add TOML struct tags and a TOML-aware migrate import path; rewrite setup/scaffold templates for TOML
- Sweep SKILL.md files, agent definitions, and architecture docs for the new config file names
- Hand-craft canonical config.toml/local.toml and retire config.json
- Fix legacy-detection regressions surfaced by the TOML migration
- Port CI scaffold scripts from JSON to TOML config
- Update setup and guardrails documentation for the new config format
- Fix the integration test suite's stale config.json fixture

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
