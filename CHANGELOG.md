# Changelog

## [0.4.1] - 2026-10-10

### RC 1

- Add a live design preview for the sdlc dashboard page, with a dashboard-design project skill and task design tasks
- Add start rule, status and stop routes, API proxy, and data markers to the design preview
- Repair dangling worktree state links at session start in the main worktree and in linked worktrees
- Name the link, the target, and the recovery in the archive and preplan errors for a dangling link
- Sync the worktree link specs and update the linked-worktree, dashboard, and smoke-test docs

### RC 2

- Dashboard run details show the duration of each pipeline step
- Dashboard shows a received-review station with a fixes tile and review cards
- received-review step writes fix-progress records for the dashboard
- Refreshed glass style for the dashboard and an updated design system
- Final fix status is kept, and damaged healing data is rejected
- Dashboard docs updated for step durations, the archive icon, and received-review fixes

## [0.4.0] - 2026-10-09

### RC 1

- Runs that wait for an answer show "WAITING ON YOU", a waiting count, and a count in the tab title.
- Plan, review, and execute stations show planned work: guardrail counts, all review dimensions, all waves.
- Plan review keeps distinct totals, the repair-limit flag, and each finding's choice.
- Ship skips the commit agent when the tree is clean.
- New `/sdlc:openspec-save` skill saves an OpenSpec change in its own PR.
- Guardrails and review dimensions are stricter.

### RC 2

- Add the preplan skill to gather context before planning, and a faster plan dispatch.
- Add run archive and cache clear to the dashboard, with a step detail viewer and glass-style dialogs.
- Let harden read custom instructions from the project, and reject unknown keys in the [harden] config section.
- Fix the OpenSpec stage check.
- Dashboard routes return 400 instead of 404 for invalid requests, and error messages use sentence case.
- Fix the pre-push hook so its tests no longer write to the real repository when you push from a linked worktree.

## [0.3.5] - 2026-10-08

### RC 1

- Promote workflow: a bump level above the RC series now prints a NOTICE and ships the RC commit under the higher version. A level below the series fails and names the level to use. "Nothing to promote" now says how to continue.
- Review runs its dimensions in waves of review.maxParallelDimensions instead of capping the number of dimensions. Review and ship now name every worker that could not be stopped.
- Ship steps and quick profile are on/off tables with a fixed step order. Harden runs after archive-openspec.
- BREAKING: ship.steps and ship.quick change from lists to tables. An old list fails ship_prepare with an error that points to /setup --only ship.
- BREAKING: review.maxDimensions is renamed to review.maxParallelDimensions. The old key fails review_prepare with an error that names the new key.
- New guardrails, review dimensions and Copilot instructions cover no-op outcomes, worker cleanup, jump routes, config key combinations and template comments.

## [0.3.4] - 2026-10-08

### RC 1

- Release candidate: add a local pipeline dashboard. One page on the loopback address shows the pipelines, sessions, learnings, and deferred items of every registered repo, with live updates.
- Add the /sdlc:dashboard skill (--status, --stop, --no-open), the dashboard MCP tool, and the sdlc dashboard serve command.
- Add the [dashboard] local config section (autoStart, port). Session start registers the repo and can auto-start the server.
- Fix the MCP integration test so its expected tool list includes the dashboard tool.
- Add dashboard specifications and documentation, and tighten the review documentation and config-verification standards.

### RC 2

- Redesign the local pipeline dashboard as the "sdlc signal room": header tabs (Pipelines, Activity, History), one repository filter, one block per pipeline with a station track and detail tiles, and hash links.
- Show execute waves with tasks, task status, and commit state; review dimensions with finding counts and totals; plan explorers and review rounds.
- Join ship, execute, and review runs into one ship block with a plan station built from the stored explorer summary.
- Split issue location into file, line, and reference, and add state issues, task errors, and stalled runs as issue sources.
- Add run history with the 50 newest rows, and write one failure row when a ship run fails.
- Keep the review ledger after a run (the 7-day sweep removes it), store planned task names at execute init, and write run metadata at the first ledger check-in.
- Record each plan review round with the new review-round marker, and return blockingCount from merge_results.
- Clarify the plan single-reviewer merge flow, add TaskStop handling for stalled writers, and add review quality gates for prose, routing, and worker verification.
- Document the snapshot contract and page specs in the dashboard docs.

## [0.3.3] - 2026-10-07

### RC 1

- setup_prepare gains an explain mode, and every setup option now carries help text with details and examples.
- Config writes keep user comments: key-level splice, a refusal (ErrWouldDropComments) instead of silently dropping comments, and restored template tips.
- The [ship] rebase schema accepts 5 values; migrate and setup_write_sections report a recovery path when a write is refused.
- The plan skill now authors OpenSpec artifacts at the end of Step 6 (late authoring), with an OpenSpec-Create header until staging.
- Fixed /ship acting on the wrong step when the pipeline lists a step twice (the two commit steps), and harden re-running clusters it already handled.

### RC 2

- Fix: review_prepare's dimension cap no longer inverts severity order — it now keeps the most severe dimensions active instead of the least severe.
- Add: configurable `[review] maxDimensions` key in `.sdlc-v2/local.toml` (default 8, minimum 1) to raise the review dimension cap per project.
- Add: dry-run and posted review output now names every queued dimension and the active cap, and flags partial coverage in the verdict heading.

### RC 3

- Fix ship run report misrepresenting user-input prompts and answers.
- Harden learned guardrails by repairing them on any validation finding, instead of reverting them.
- Fix setup losing tip comments when it writes config.
- Add a user-level (cross-project) config layer for personal settings.

## [0.3.2] - 2026-10-03

- Add internal/commstyle package for ASD-STE100 style checking, writing-guide generation, and style metrics for plan documents
- Added OpenSpec specs for user-input tracking and report layout, and archived the completed plan-openspec-and-run-improvements change
- Added a base-branch-aware pipeline with OpenSpec staging and base-sync support
- Apply 5 automated hardening passes that strengthened guardrails, review dimensions, and Copilot instructions based on this change's own review findings
- Clarified release-workflow sync wording
- Fixed worktree-aware tooling for setup_init, setup_write_sections, execute-report duration, and ship-report user-input recording, with new test coverage for setup_init's root splitting
- Hardened the release pipeline: safer promote-release (env-based inputs, active-worktree-aware reads), more robust release-on-main failure/ref/concurrency handling, and changelog validation against the latest final tag
- Removed duplicated retag-release logic, now consolidated into a single implementation
- Update related schemas and templates to support the new style checks
- Wire style checks into the plan, execute, review, commit, and PR skills and their MCP tooling

## [0.3.1] - 2026-10-02

### RC 1

- Plan, execute, and ship now share one configured base branch for rebases and base-relative diffs
- Execute merges the base branch into the working branch between waves, with automatic conflict resolution by a sub-agent
- Plan mode stages real OpenSpec proposal/design/spec content via the OpenSpec CLI; ship and execute materialize and validate it into an actual OpenSpec change automatically
- Ship's report now includes a Planning section and a full plan-to-ship timeline; completed plan runs are cleaned up only after the report is written
- Gate A guardrail checks now flag conflicts between plan guardrails and OpenSpec design/tasks content
- `task deploy` prunes stale cached binaries instead of accumulating them
- Full code-review remediation pass (18 findings fixed) plus a guardrail-hardening pass across the pipeline

### RC 2

- Added a base-branch-aware pipeline with OpenSpec staging and base-sync support
- Hardened the release pipeline: safer promote-release (env-based inputs, active-worktree-aware reads), more robust release-on-main failure/ref/concurrency handling, and changelog validation against the latest final tag
- Removed duplicated retag-release logic, now consolidated into a single implementation
- Fixed worktree-aware tooling for setup_init, setup_write_sections, execute-report duration, and ship-report user-input recording, with new test coverage for setup_init's root splitting
- Added OpenSpec specs for user-input tracking and report layout, and archived the completed plan-openspec-and-run-improvements change
- Clarified release-workflow sync wording

## [0.3.0] - 2026-10-01

- Added config guardrails for error enrichment and test coverage
- Added error recovery around gh label add/remove operations
- MCP annotations and input schemas corrected; skill and docs aligned with tool behavior
- OpenSpec initialized; strict baseline specs for all 13 skills and 31 MCP tools
- commit: raw (also non-ASCII) file names; staged-only changes no longer listed as unstaged; stale manifest temp dirs removed
- execute / execute_state: wave-await branch from the active worktree; working recovery for a diverged wave commit; bad decision input rejected; schema matches written state
- harden, error-report, jira, links, learnings, telemetry: reference, parsing, numbering and labelling fixes; jira drafts from a base structure when no template exists
- plan / plan_support: skill sends the field names the tool reads; failed lanes block the merge; lens and writer counts fixed; empty task fields detected in all field checks
- polling: gh pr checks errors no longer read as a green pipeline; auth/404 classified; no-CI poll ends as skipped
- pr: pr_apply edits only an open PR; --draft and --base pass through to gh pr create; pr_prepare keeps warnings on auth exits and warns on an unreadable version config
- pr_apply now records release intent (level + optional pre-release) only, no pinned version number, writing notes under an "## [Unreleased]" heading
- pr_apply replaces any stale release:* label with the new one on re-apply instead of accumulating labels
- review: open PR detection; no PR lookup for local and worktree scopes; git and folder read failures reported; truncated and queued dimensions handled correctly
- security: dimensions_render_instructions blocks path escape through the frontmatter name
- setup / config / migrate: section writes keep comments, CRLF and integers; unknown keys rejected; legacy import only overwrites untouched template defaults; dry run uses the real stale check
- ship / ship_state: side effects recorded during the run; only an open PR counts; non-retryable poll errors stop the loop; GC dry run matches the real run; conditional steps and bad outcomes rejected
- tests no longer leave temp folders or integration binaries behind

## [0.2.1] - 2026-09-30

### RC 1

- pr_apply now records release intent (level + optional pre-release) only, no pinned version number, writing notes under an "## [Unreleased]" heading
- pr_apply replaces any stale release:* label with the new one on re-apply instead of accumulating labels
- Added error recovery around gh label add/remove operations
- Added config guardrails for error enrichment and test coverage

## [0.2.0] - 2026-09-30

### RC 1

- Add checkpoint-based session recovery for the /plan skill, including a new evidence store (replacing the earlier "ledger" naming)
- Add resume mode to plan preparation with post-compact detection, plan-style instructions, and file-path guardrails in lane/lens/reviewer prompts
- Add evidence store actions and checkpoint markers, with evidence-directory cleanup when a plan session stops
- Document the evidence store, resume, checkpoint, and custom plan instructions concepts
- Add a reference documentation accuracy review dimension

### RC 2

- Add checkpoint-based recovery for context compaction during plan execution (#58)
- Add a new ship "harden" step: review-driven hardening applied and committed separately, before PR creation
- Make received-review --auto account for every review finding (fix or explicit defer); add --no-harden opt-out
- Add shared review-cluster rules for grouping related findings consistently
- Add ship_state MCP actions (healing_record, harden_clusters, report) for self-healing tracking and MCP-composed run reports with plan-duration timing
- Wire the harden step into the ship pipeline docs; default review threshold to "info" and validate it
- Fix review findings raised against the harden step and run report implementation

## [0.1.8] - 2026-09-29

- Add scheduled weekly workflow that deletes old GitHub Releases and their binary assets, keeping the 10 most recent releases (git tags are preserved).
- Added GH013 (ruleset rejection) error classification with secret-name-aware remediation hints
- CI trigger is dispatch-only (dropped tag-push) and now runs the full Node test suite
- Documented protected-branches-and-rulesets behavior in the versioning docs
- Fixed 15 code-review findings: secret-name validation for generated CI files, restored push-with-secret guidance text, corrected schema permission description, added default/pattern validation for the secret-name field, fixed Node 21+ test glob
- Hardened review checklists and execute/plan guardrails based on review findings
- Moved personal per-developer config keys to local.toml; execute step now resolves auto/quality/highRiskAutoApprove from config
- Release pipeline now falls back through a GitHub App token, then RELEASE_TOKEN, then GITHUB_TOKEN, so releases keep working under GitHub rulesets that block direct pushes
- promote-release can now deliver its commit via a pull request instead of requiring a direct push
- retag-release now guards against retagging non-release commits or running outside main

## [0.1.7] - 2026-09-28

### RC 1

- Add stateless `resolve-config` action to `execute_state` plus new config schema/template keys, so execute mode's auto/quality/highRiskAutoApprove settings can come from `.sdlc-v2/config.toml`
- Rewire execute skill's quality-tier and high-risk-wave prompt gates to resolve from project config first, only falling back to interactive prompts when undecided (with resolution-matrix tests)
- Fix skillcheck's line-number-keyed pins after the execute SKILL.md rewire
- Consolidate config reads in execute and clarify output; harden config-driven execute mode (22 review findings fixed): validate CLI `--quality` input, distinguish "not found" from real config read errors, and warn loudly when a high-risk wave auto-proceeds from committed config

### RC 2

- Move per-developer config keys (pr.expectedAccount, execute.auto/quality/highRiskAutoApprove) out of committed .sdlc-v2/config.toml into gitignored .sdlc-v2/local.toml
- Add automatic migration (internal/configmigrate/movedkeys.go) for repos with personal keys still in the old committed config
- Fix nil-pointer-in-interface bug: MigrateMovedKeys now returns a plain error instead of a concrete *MovedKeysErr pointer
- Harden pr_prepare warning/error reporting around the migration path, sync review-dimension guardrails and skill docs

### RC 3

- Release pipeline now falls back through a GitHub App token, then RELEASE_TOKEN, then GITHUB_TOKEN, so releases keep working under GitHub rulesets that block direct pushes
- Added GH013 (ruleset rejection) error classification with secret-name-aware remediation hints
- promote-release can now deliver its commit via a pull request instead of requiring a direct push
- retag-release now guards against retagging non-release commits or running outside main
- CI trigger is dispatch-only (dropped tag-push) and now runs the full Node test suite
- Documented protected-branches-and-rulesets behavior in the versioning docs
- Fixed 15 code-review findings: secret-name validation for generated CI files, restored push-with-secret guidance text, corrected schema permission description, added default/pattern validation for the secret-name field, fixed Node 21+ test glob
- Hardened review checklists and execute/plan guardrails based on review findings
- Moved personal per-developer config keys to local.toml; execute step now resolves auto/quality/highRiskAutoApprove from config

## [0.1.6] - 2026-09-22

### RC 1

- chore(harden): strengthened plan and execute guardrails and review dimensions from a 26-entry learnings-log triage (13 plan guardrails, 17 execute guardrails, 10 review dimensions — 7 edited, 3 new — plus 3 new copilot-instructions mirrors)

### RC 2

- MCP tools now return human-readable Markdown instead of a JSON envelope, via one generic reflection renderer.
- Tool errors now state what failed and how to recover.
- Skills, agents and review guardrails rewritten for the Markdown output; new contract doc at docs/mcp-output-contract.md.
- `execute_state` warns on unknown task ids, `ship_verify_side_effect` always shows the expected line, and `default-rc` pre-release policy works in release diagnostics and the setup wizard.

### RC 3

- feat(tooling): improve error handling and deferred findings persistence — review-fix follow-up addressing 84 findings from received-review (81 fixed, 3 recorded as needs-direction deferrals)
- feat(tooling): add error fixes and deferred skill — sweeps tooling error handling and adds a skill for recording findings that need human direction
- feat(mcp): return Markdown from MCP tools, drop the JSON envelope (#52)
- chore(harden): apply 26-entry learnings triage across guardrails and review dimensions (#44)

## [0.1.5] - 2026-09-18

- Fixes:
- Reclaim message now uses a valid phase enum instead of a free-text placeholder.
- Release notes (patch)
- SDLC config paths now anchor to worktree roots instead of resolving relative to cwd.
- Wave-await liveness check now treats a reclaim reply as proof-of-life instead of a failure, with updated thresholds and docs.
- Wave-liveness PostToolUse hook now stamps per-task progress on Edit/Write, confirmed to fire inside Task-tool-dispatched subagents.
- fix(config): anchor SDLC paths to worktree roots
- fix(tools): address review findings for worktree-anchor-sdlc-paths
- fs-writer seam and progress-touch tracking added for wave-start runs; tests now require this injectable seam instead of touching real fs/git.

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
