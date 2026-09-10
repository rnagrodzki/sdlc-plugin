# /received-review

Respond to code review feedback on a pull request. Reads reviewer comments,
verifies each one against the actual code, and applies fixes or replies with
technical justification.

## When to use

- A reviewer (human or automated) left comments on your PR and you want to
  work through them systematically.
- `/ship` triggered an automatic fix loop because the review found critical
  or high-severity issues.
- You want review feedback addressed with technical rigor, not surface-level
  agreement.

## Syntax

    /received-review [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--pr <number>` | PR number to process feedback for. | auto-detected from branch |
| `--auto` | Apply fixes without asking for confirmation. | off |

## Examples

**Process feedback on the current branch's PR:**

    /received-review

Detects the PR, reads all comments, verifies each against the code, and works
through them one by one.

**Process feedback for a specific PR:**

    /received-review --pr 42

**Process feedback without prompts (used by pipelines):**

    /received-review --auto

## Related skills

- [/review](review.md) — Produces the findings that this skill responds to.
- [/commit](commit.md) — Commit the fixes after addressing feedback.
- [/ship](ship.md) — Runs this skill when review findings exceed the severity
  threshold.

## Tips and gotchas

- **Dual self-critique.** The skill verifies each comment against the actual
  code before accepting it. If a comment is incorrect, it explains why instead
  of blindly agreeing.
- **Not just for human reviewers.** This skill processes feedback from any
  source: humans, Copilot, or `/review`'s own findings.
- **Fix loop in /ship.** When running inside `/ship`, this skill triggers
  conditionally — only if `/review` findings exceed the configured severity
  threshold.
