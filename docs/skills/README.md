# SDLC Skills Reference

This directory contains user-facing documentation for each SDLC plugin skill. For an overview of when to use each skill, see [`../getting-started.md`](../getting-started.md).

## Skills by stage

### Planning
- [`plan`](plan.md) — Decompose a requirement into an implementation plan with tasks and dependencies.

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

### Setup
- [`setup`](setup.md) — Initialize or reconfigure SDLC plugin settings for a project.

## Pipelines

For end-to-end automation, use one of the pipeline skills:

- [`ship`](ship.md) — Automated workflow: execute plan → commit → review → open PR → verify CI (with confirmation between steps).

## Links to full documentation

- Plugin overview: [`../README.md`](../README.md)
- Getting started: [`../getting-started.md`](../getting-started.md)
- Smoke test (verification checklist): [`../smoke-test.md`](../smoke-test.md)
- Configuration reference: [`../versioning.md`](../versioning.md)
