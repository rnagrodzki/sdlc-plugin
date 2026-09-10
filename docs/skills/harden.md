# /harden

Analyze a pipeline failure and propose user-approved edits to the project's
hardening surfaces — plan/execute guardrails, review dimensions, or Copilot
instructions — so the same class of failure is caught earlier next time.
Strengthen-only: it never relaxes or removes an existing rule.

## When to use

- A pipeline step (`/plan`, `/execute`, `/review`, ...) just failed and you
  want to prevent the same failure from recurring.
- You want to turn a one-off mistake into a durable guardrail or review
  dimension instead of just fixing the immediate symptom.
- Another skill's failure-handling menu offered a "harden" option and you
  picked it.

## Syntax

    /harden --failure-text <text> --skill <name> [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--failure-text <string>` | The failure to analyze, as free text. Required unless `--from-issue` is used; mutually exclusive with it. | — |
| `--from-issue <num>` | Analyze a GitHub issue instead of inline text. Mutually exclusive with `--failure-text`. | — |
| `--skill <name>` | Name of the skill that failed. Always required. | — |
| `--step <s>` | Step name within the failing skill, if known. | — |
| `--operation <op>` | Operation being performed when the failure occurred. | — |
| `--exit-code <code>` | Exit code or HTTP status from the failure, if any. | — |
| `--error-type <type>` | Category of error, if known. | — |
| `--user-intent <text>` | What the user was trying to accomplish. | — |
| `--args-string <text>` | The invocation arguments that led to the failure. | — |

## Examples

**Analyze a failure from `/plan`:**

    /harden --failure-text "Step 5 reviewer loop exceeded max retries without converging" --skill plan --step "Step 5" --operation "reviewer-loop"

Loads the failure context, classifies it as user-code, plugin-defect, or
ambiguous, and — for user-code or ambiguous failures — proposes guardrail or
review-dimension edits you can apply, skip, or cancel one at a time.

**Analyze a failure already filed as a GitHub issue:**

    /harden --from-issue 42 --skill execute

Fetches the issue body and (if labeled `mcp-failure`) treats it as a
pre-classified plugin defect, skipping straight to the `error-report` route.

## Related skills

- [/error-report](../../plugins/sdlc/skills/error-report/SKILL.md) — Where
  `/harden` routes failures it classifies as plugin defects.
- [/setup](setup.md) — Author the initial guardrails and review dimensions
  that `/harden` later strengthens.
- [/review](review.md) — Review dimensions are one of the surfaces `/harden`
  can add to or strengthen.
- [/plan](plan.md) and [/execute](execute.md) — Their guardrails are two of
  the surfaces `/harden` can strengthen.

## Tips and gotchas

- **Strengthen-only.** `/harden` never proposes relaxing or removing an
  existing rule — every proposal adds or tightens a guardrail, dimension, or
  instruction.
- **Nothing is written without approval.** Each proposal is presented
  individually with a patch preview; you choose apply, skip, or cancel per
  proposal. Cancelling stops the whole run without applying later proposals.
- **`--failure-text` and `--from-issue` are mutually exclusive.** Provide
  exactly one, not both.
- **Not directly part of `/ship`.** `/ship` does not call `/harden` itself —
  it delegates failure handling to whichever sub-skill failed, and that
  sub-skill's own failure menu offers `/harden` as an option.
- **New review dimensions get a Copilot mirror automatically.** When a
  proposal adds a brand-new review dimension, `/harden` generates the
  matching Copilot instructions file for you — existing dimensions are not
  retroactively mirrored.
