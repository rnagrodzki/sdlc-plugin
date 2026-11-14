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

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 1: CONSUME — parse args, load logs (R1, R6) | tool | Reads `--pr`, `--logs` and `--auto`. With `--pr` only, it gets the failing check log with `gh`. It stops with an `abort` verdict when no log is found. | The log text | no | always |
| Step 2: CLASSIFY — invoke the classification tool (R2) | tool | Calls `verify_pipeline_classify` with the log text. | The category and the matched signals | no | always |
| Step 3: PROPOSE OR APPLY (R3, R4, R9) | skill | For `lint`, `test-failure` and `type-error` with `--auto`, it edits the source files. For every other case it writes a proposal. It runs no `git commit` or `git push`. | The fix or the proposal | no | always |
| Step 4: VERDICT — single JSON line on stdout (R5) | skill | Prints one JSON line: `fix-applied`, `proposal` or `abort`. | The verdict line for the caller | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/verify-pipeline`. `--pr <number>` or `--logs <path-or-text>` is required. `/ship` runs it when a CI poll fails under `automation.mode: auto`. |
| Ends | After Step 4 prints the verdict line. |
| Asks the user | Nothing. The skill has no prompt. |
| Resume | none |

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
