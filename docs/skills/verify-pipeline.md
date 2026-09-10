# /verify-pipeline

Analyze a failed CI run on a pull request. Classifies the root cause and
either applies a minimal fix or reports a proposal.

## When to use

- A CI check failed on your PR and you want to understand why and fix it.
- `/ship`'s post-PR step detected a CI failure.
- You have CI log output and want to classify the failure type.

## Syntax

    /verify-pipeline [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--pr <number>` | PR number whose CI checks to analyze. | none |
| `--logs <path-or-text>` | Path to a log file or inline log text. | none |
| `--auto` | Apply fixes without asking for confirmation. | off |

At least one of `--pr` or `--logs` is required.

## Examples

**Analyze a failing PR:**

    /verify-pipeline --pr 42

Fetches CI results, downloads logs for failed checks, classifies the root
cause, and proposes a fix.

**Analyze from a log file:**

    /verify-pipeline --logs build-output.log

**Fix automatically (used by pipelines):**

    /verify-pipeline --pr 42 --auto

## Related skills

- [/ship](ship.md) — Optionally runs this skill after creating a PR.
- [/harden](harden.md) — For systemic prevention; this skill handles the
  immediate fix.
- [/pr](pr.md) — The PR whose CI this skill analyzes.

## Tips and gotchas

- **One-shot, not a watcher.** Runs once and stops. Does not wait for CI to
  finish — if CI is still running, wait first or let `/ship` handle polling.
- **At least one input required.** Provide `--pr`, `--logs`, or both.
- **Minimal fixes.** Applies the smallest change to make CI pass. Does not
  refactor or improve beyond what is needed.
- **Works standalone or in pipelines.** Run directly or let `/ship` invoke it
  via the `verify-pipeline` step.
