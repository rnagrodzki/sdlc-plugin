# /pr

Generate a PR description and open it via GitHub CLI. When your project
tracks a version, this skill also diagnoses version state and can resolve a
release bump as part of opening the PR.

## When to use

- You have committed changes on a feature branch and want to open a PR.
- You want to update an existing PR's description to reflect new changes.
- You want a well-structured PR description generated automatically.
- Your project tracks a version and you want to decide on (or explicitly
  skip) a release bump while opening the PR.

## Syntax

    /pr [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--auto` | Skip the approval prompt and publish immediately. Standalone use requires a release decision to already be resolved — see Tips. | off |
| `--draft` | Create the PR as a draft. Applies only when a new PR is created. | off |
| `--base <branch>` | Target base branch for a new PR. Applies only when a new PR is created. | the repository's default branch |
| `--skip-approval` | Skip only the Step 5 publish-confirmation prompt (e.g. when dispatched by ship, which already resolved release intent). Not equivalent to `--auto`. | off |

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0 — Plan Mode Check | skill | Stops the run when plan mode is active, because the skill creates or updates a PR. | The go-ahead for the next step | no | always. It stops the run only in plan mode |
| Step 0 (CONSUME): Call `pr_prepare` | tool | Calls `pr_prepare`. Shows each error and stops when `PR_CONTEXT.ok` is `false`. Shows each warning. | `PR_CONTEXT` | no | always |
| Step 1: Consume the Context | skill | Reads the branch, the Jira ticket, the template, the commits and the version data. Holds the release fields that the caller gave. | The facts for the description and the release intent | no | always |
| Step 1b (GATE): Release Intent | skill | Asks for a release level, or for an acknowledged skip, and drafts the release notes. With `--auto` and no release level it stops with an error. | The release level, the release notes and the release source | yes | no `releaseLevel` was given at invocation |
| Step 2 (PLAN): Draft PR Description | skill | Drafts the title and every section of the PR template from the commits of the branch. | The draft title and description | no | always |
| Step 3 (CRITIQUE): Self-review the Draft | skill | Checks the draft against the quality gates. | A list of failing gates | no | always |
| Step 4 (IMPROVE): Revise Based on Critique | skill | Fixes each failing gate, with at most 2 rounds for each gate. Asks the user a question when a business section stays unclear. | The revised draft | no | always |
| Step 5 (DO): Present for Review | skill | Shows the release intent, the title and the description. Asks `yes`, `edit` or `cancel`. | The approved title and description | no | always. `--auto` and `--skip-approval` skip the question |
| Step 6: Create or Update PR | tool | Checks the links in the body with `links_validate`, then calls `pr_apply` to create the PR or update the open PR. | The PR URL | no | always, after `yes` or after a skipped question |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/pr`. `--draft`, `--base <branch>`, `--auto` and `--skip-approval` change the run. |
| Ends | After Step 6 shows `Pull request created` or `Pull request updated` with the URL. A `cancel`, an error or a link violation ends the run with no PR change. |
| Asks the user | At Step 1b for the release level. At Step 5: `yes`, `edit` or `cancel`. At Step 4 when a business section stays unclear. |
| Resume | none |

## Examples

**Create a PR with interactive review:**

    /pr

Generates a description, asks whether this PR should trigger a release (or
whether to explicitly skip one), shows everything for approval, then creates
the PR. If a PR already exists for this branch, it updates instead.

**Create a PR without prompts (inside `/ship`):**

    /pr --auto

Skips the approval prompt. Only works when the release decision is already
resolved, which in practice means running inside `/ship` — it resolves that
decision itself before calling `/pr`. Typing `/pr --auto` directly, with no
pipeline behind it, stops with an error telling you to use `/ship` instead.

**Update an existing PR's description:**

    /pr

Run it again on the same branch. When the branch already has an open PR, the
skill regenerates its title and description and edits that PR. Useful after
pushing additional commits.

## Related skills

- [/commit](commit.md) — Commit changes before opening a PR.
- [/review](review.md) — Review changes before creating the PR.
- [/ship](ship.md) — Runs `/pr` as its final main step.
- [/verify-pipeline](verify-pipeline.md) — Analyze CI failures after the PR
  is created.

## Tips and gotchas

- **Requires the `gh` CLI.** Make sure `gh auth login` is done before running.
- **Create vs. update is automatic.** The skill detects whether a PR exists for
  the current branch. There is no `--update` flag.
- **Must be on a feature branch.** The skill refuses to run on `main`/`master`.
- **`--draft` and `--base` only affect a new PR.** When the branch already has
  an open PR, the skill updates it and tells you each of these flags was
  ignored: `gh pr edit` cannot make a PR a draft or change its base. Use
  `gh pr ready --undo` or GitHub's UI for that.
- **No custom labels.** The only label the skill applies is the `release:*`
  label that records the release decision. Use `gh pr edit --add-label` for
  any other label.
- **You may be asked about a release bump.** If your project tracks a version
  and no release decision was made upstream (e.g. by `/ship`), the skill asks
  whether to bump the version (patch/minor/major, optionally as a release
  candidate) before showing the PR for approval. You can explicitly skip this
  instead of picking a level.
- **`--auto` needs a resolved release decision.** Outside of `/ship`, there is
  no way to answer the release-bump question non-interactively, so a bare
  `/pr --auto` stops with an error rather than guessing. Use `/ship --auto`
  for unattended runs.
- **Links are checked before publishing.** Every link in the generated
  description is verified; a broken link blocks the PR from being created
  until it's fixed.
