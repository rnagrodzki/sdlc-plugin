# /received-review

Respond to code review feedback on a pull request. Reads reviewer comments,
verifies each one against the actual code, and applies fixes or replies with
technical justification.

## When to use

- A reviewer (human or automated) left comments on your PR and you want to
  work through them systematically.
- `/ship` triggered an automatic fix loop because the review found issues at
  or above the project's configured severity threshold.
- You want review feedback addressed with technical rigor, not surface-level
  agreement.

## Syntax

    /received-review [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--pr <number>` | PR number to process feedback for. | auto-detected from branch |
| `--auto` | Run without prompts. Only "agree — will fix" ends a finding; every other outcome is recorded as a `needs-direction` follow-up instead of being closed. See [Auto mode](#auto-mode). | off |

## Examples

**Process feedback on the current branch's PR:**

    /received-review

Detects the PR, reads all comments, verifies each against the code, and works
through them one by one.

**Process feedback for a specific PR:**

    /received-review --pr 42

**Process feedback without prompts (used by pipelines):**

    /received-review --auto

## Auto mode

`--auto` is for pipeline and subagent dispatch, where there is no human to
ask. It does not mean "agree with everything and move on". It narrows what is
allowed to close a finding down to one verdict:

- **`agree — will fix`** — the only outcome that ends a finding. The fix is
  made and the review thread gets a reply saying so.
- **Everything else becomes `needs-direction`** — recorded, not closed.
  "Agree, won't fix", "disagree" and "cannot verify" all land here. The thread
  gets a reply naming the open question, and the finding goes to the backlog.

Either way the thread itself is only replied to, never resolved: this port
does not resolve review threads programmatically, so close them yourself in
the GitHub UI once you are satisfied.

A recorded finding keeps the judgment that was actually reached, in a separate
`reason` field (`wont-fix`, `disagree` or `needs-direction`), so whoever reads
the backlog later can tell a finding the skill thought was wrong from one that
is a real fork in the road.

`needs-direction` is only valid when the record names **two or more** candidate
approaches plus a one-line trade-off between them. One obvious approach is not
a question — it is a fix.

**Where the records go.** They are written once verdicts are final, not at the
moment a verdict is first reached — a verdict can still change later in the
run, and the deferred file is append-only, so an early write would leave a
stale entry behind. They show up in the deferred backlog that
[/deferred](deferred.md) triages, and in the end-of-run ledger that accounts
for every finding as `fixed`, `deferred` (grouped by `reason`), or
`UNACCOUNTED`.

**Recording is best-effort, with a fallback.** The primary write attaches the
finding to the current `/ship` run. When there is no ship state for the branch
— a standalone run, for instance — or the write cannot be persisted, the skill
falls back to writing straight to the deferred file instead. Only if both fail
is the finding named `UNACCOUNTED` in the ledger. It is never silently dropped.

**It also reaches `/harden`.** Step 11.6 clusters the findings by file and
dispatches [`/harden --auto`](harden.md) for each cluster (capped at 5), which
applies strengthen-only guardrail and review-dimension edits to your project
without a confirmation prompt. Each edit is listed under "Auto-accepted" in the
summary this skill relays back.

## Related skills

- [/review](review.md) — Produces the findings that this skill responds to.
- [/commit](commit.md) — Commit the fixes after addressing feedback.
- [/ship](ship.md) — Runs this skill when review findings meet or exceed the
  severity threshold.
- [/deferred](deferred.md) — Triages the findings this skill records instead
  of fixing.
- [/harden](harden.md) — Dispatched per finding cluster in Step 11.6 to turn
  recurring findings into project guardrails.

## Tips and gotchas

- **Dual self-critique.** The skill verifies each comment against the actual
  code before accepting it. If a comment is incorrect, it explains why instead
  of blindly agreeing.
- **Not just for human reviewers.** This skill processes feedback from any
  source: humans, Copilot, or `/review`'s own findings.
- **Fix loop in /ship.** When running inside `/ship`, this skill triggers
  conditionally — only if `/review` produced at least one finding that meets
  or exceeds the configured severity threshold. Findings below it are deferred
  instead, not dropped.
