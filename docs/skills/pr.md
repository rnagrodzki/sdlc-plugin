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
| `--update` | Signals you're updating an existing PR. Has no practical effect: the skill auto-detects create vs. update either way. | off |
| `--draft` | Create the PR as a draft. **Not yet functional.** | off |
| `--base <branch>` | Target base branch. **Not yet functional.** | auto-detected |
| `--label <name>` | Add a label to the PR. **Not yet functional.** | none |

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

    /pr --update

Regenerates the description. Useful after pushing additional commits.

## Related skills

- [/commit](commit.md) — Commit changes before opening a PR.
- [/review](review.md) — Review changes before creating the PR.
- [/ship](ship.md) — Runs `/pr` as its final main step.
- [/verify-pipeline](verify-pipeline.md) — Analyze CI failures after the PR
  is created.

## Tips and gotchas

- **Requires the `gh` CLI.** Make sure `gh auth login` is done before running.
- **Create vs. update is automatic.** The skill detects whether a PR exists for
  the current branch. You do not need `--update` explicitly.
- **Must be on a feature branch.** The skill refuses to run on `main`/`master`.
- **Some flags are not yet functional.** `--draft`, `--base`, and `--label` are
  listed but not yet supported. Use GitHub's UI or `gh` directly for these.
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
