# SDLC Skills Reference

This directory contains user-facing documentation for each SDLC plugin skill. For an overview of when to use each skill, see [`../getting-started.md`](../getting-started.md).

## Skills by stage

### Planning
- [`preplan`](preplan.md) — Shape an idea into a topic file, with each proposal checked against the plan guardrails, before you plan it.
- [`verify-requirements`](verify-requirements.md) — Check requirements from a topic file, a file, a Jira ticket or a GitHub issue, and report the findings before you plan.
- [`plan`](plan.md) — Decompose a requirement into an implementation plan with tasks and dependencies.
- [`openspec-save`](openspec-save.md) — Save the OpenSpec change that plan staged as its own branch, commit, and PR. Merge it first, then ship.

### Implementation
- [`execute`](execute.md) — Run a plan wave by wave, with per-wave verification.

### Committing
- [`commit`](commit.md) — Generate a commit message matching the project's style and commit staged changes.

### Reviewing
- [`review`](review.md) — Multi-dimension code review (security, performance, docs, etc.) of the current diff.
- [`received-review`](received-review.md) — Work through reviewer or CI feedback on an open PR.

### Pull Requests
- [`pr`](pr.md) — Generate a PR description and open it via GitHub CLI; diagnose version state when applicable.

### Verification
- [`verify-pipeline`](verify-pipeline.md) — Diagnose and optionally fix a failing CI run on a PR.

### Jira Integration
- [`jira`](jira.md) — Create, read, or update Jira issues linked to your work.

### Hardening
- [`harden`](harden.md) — Propose guardrail changes to prevent the same pipeline failure from recurring.

### Follow-ups
- [`deferred`](deferred.md) — List deferred items from earlier runs, then file them as GitHub issues or resolve them without filing.

### Monitoring
- [`dashboard`](dashboard.md) — Open the local sdlc dashboard showing every repo's pipeline progress, session activity, and issues.

### Setup
- [`setup`](setup.md) — Initialize or reconfigure SDLC plugin settings for a project.

### Error reporting
- [`error-report`](error-report.md) — File a tooling error as a GitHub issue, after two consent gates and a check for similar open issues.

## Pipelines

For end-to-end automation, use one of the pipeline skills:

- [`ship`](ship.md) — Automated workflow: execute → commit → review → PR, with optional OpenSpec, harden, CI and remote review steps.

## Skill map

The role `pipeline` marks a skill that is a stage on the path from an idea to a merged change. The role `around` marks a skill that supports that path from outside.

| Skill | Role | Summary |
|---|---|---|
| preplan | pipeline | Shapes an idea into a topic file. |
| verify-requirements | pipeline | Checks requirements and reports the findings before you plan. |
| plan | pipeline | Turns requirements into a plan with tasks and dependencies. |
| openspec-save | around | Saves the staged OpenSpec change as its own branch, commit and PR. |
| execute | pipeline | Runs a plan wave by wave, with verification after each wave. |
| commit | pipeline | Writes a commit message in the style of the project and commits. |
| review | pipeline | Reviews the diff across the review dimensions of the project. |
| received-review | pipeline | Works through reviewer and CI feedback on an open PR. |
| pr | pipeline | Writes the PR description and opens or updates the PR. |
| verify-pipeline | pipeline | Diagnoses a failed CI run on a PR and fixes it when it can. |
| ship | pipeline | Runs execute, commit, review and PR as one pipeline. |
| harden | pipeline | Proposes guardrail edits that prevent a repeat of a pipeline failure. |
| deferred | around | Lists the items that earlier runs did not fix and files or resolves them. |
| jira | around | Creates, reads and updates Jira issues linked to your work. |
| dashboard | around | Opens the local dashboard of pipelines, sessions and issues. |
| setup | around | Sets up or changes the plugin settings of a project. |
| error-report | around | Files a tooling error as a GitHub issue. |

## Handovers

| From | To | Hands over |
|---|---|---|
| preplan | plan | Topic file ready for plan |
| plan | ship | Plan file |
| plan | execute | Plan file |
| harden | plan | Edited plan guardrails, which plan loads on its next run |
| openspec-save | commit | The staged OpenSpec change files |
| openspec-save | pr | The branch that holds the OpenSpec change |
| openspec-save | ship | The plan file, for a run after the OpenSpec PR is merged |
| ship | execute | Plan file |
| ship | commit | Changed files, with `--auto` |
| ship | review | The committed diff |
| ship | received-review | Review findings at or above the threshold |
| ship | harden | Review findings that the fix pass fixed or deferred |
| ship | pr | Branch, with the flags `--draft` and `--base` when set |
| ship | verify-pipeline | A failed CI poll, with the PR number and the check logs |

## Links to full documentation

- Plugin overview: [`../../README.md`](../../README.md)
- Getting started: [`../getting-started.md`](../getting-started.md)
- Smoke test (verification checklist): [`../internal/smoke-test.md`](../internal/smoke-test.md)
- Configuration reference: [`../versioning.md`](../versioning.md)
