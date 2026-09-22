# /review

Run a multi-dimension code review of the current diff. Each dimension
(security, performance, documentation, etc.) is reviewed independently, and
findings are reported with severity levels.

## When to use

- You have uncommitted or committed changes and want a thorough code review
  before opening a PR.
- You want to catch issues across multiple quality dimensions (security,
  performance, error handling, etc.).
- You want to preview which dimensions would run without actually running the
  review (`--dry-run`).

## Syntax

    /review [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--base <branch>` | Compare against this branch instead of the auto-detected default. | auto-detected |
| `--dry-run` | Show the review plan (dimensions, file counts) without running it. | off |

**Note:** The review scope is controlled by project configuration in
`.sdlc-v2/config.toml`, not by command-line flags. Accepted values: `all`,
`committed`, `staged`, `working`, and `worktree` — where `working` covers only
unstaged edits (local scope) and `worktree` covers all tracked changes on the
branch (branch scope). Change it with `/setup`.

## Examples

**Review all current changes:**

    /review

Detects changed files, determines which dimensions apply, and runs one
reviewer per dimension in parallel.

**Preview the review plan:**

    /review --dry-run

Shows which dimensions would run and how many files each covers — without
running any reviews.

**Review against a specific branch:**

    /review --base develop

Compares the current branch against `develop` instead of the default.

## Precomputed report data

`/review`'s preparation step returns a set of already-computed counts
alongside the dimension list, so the report header doesn't need to
re-derive them: `dimensionsTotal`, `dimensionsApplied`, `filesChanged`,
`commitCount`, `linesChanged`, `scope`, and `hasPR`. These describe the
review's *input* (what's being reviewed and with which dimensions) — finding
counts and severity breakdowns are **not** precomputed, since findings don't
exist until each dimension's reviewer lane actually runs; those are
aggregated after the lanes complete, same as before.

## Example dimension: `skill-doc-drift`

This project dogfoods its own review dimensions.
`.sdlc-v2/review-dimensions/skill-doc-drift.md` checks that each
`docs/skills/*.md` file stays in sync
with its `SKILL.md` source of truth — steps, tool calls, flags, and mandatory
gates like the state-first Step 1 described in [execute](execute.md) and
[plan](plan.md). It's a useful pattern to copy if you want docs-vs-
implementation drift caught automatically in your own project's reviews.

## Related skills

- [/received-review](received-review.md) — Respond to the findings from this
  skill.
- [/commit](commit.md) — Commit changes before or after reviewing.
- [/ship](ship.md) — Runs `/review` as its third step.
- [/setup](setup.md) — Configure review dimensions and scope.

## Tips and gotchas

- **Dimensions are project-specific.** Review dimensions come from
  `.sdlc-v2/review-dimensions/`. Run `/setup --dimensions` to add or change
  them.
- **Scope is config-driven.** You cannot pass `--staged` or `--committed` on
  the command line. The scope is set in `.sdlc-v2/config.toml`. Use `/setup` to
  change it.
- **Dry run is useful for tuning.** If reviews miss files or take too long, use
  `--dry-run` to see the plan and adjust dimensions accordingly.
- **Severity levels drive pipelines.** In `/ship`, findings at or above the
  configured threshold trigger an automatic fix loop. Lower-severity findings do
  not block. They are saved for [/deferred](deferred.md), not dropped.
