# Flags and the release decision

Read this page when you want to know each flag of `/sdlc:pr`, how the release
question works, why a run stopped, or what the skill does not do. The short
overview is [/sdlc:pr](../pr.md).

## Example

```text
/sdlc:pr --draft --base develop
```

The skill opens the new PR as a draft against `develop`. When the branch
already has an open PR, the skill updates that PR and prints a warning for
each of the two flags.

## Flags

| Flag | What it does | Default |
|---|---|---|
| `--draft` | Opens the new PR as a draft. | off |
| `--base <branch>` | Sets the base branch of the new PR. | the default branch of the repository |
| `--auto` | Skips the publish question and marks the run as unattended. It needs a release level from the caller. | off |
| `--skip-approval` | Skips only the publish question. It does not mark the run as unattended. | off |

- `--draft` and `--base` apply only when the skill creates a new PR.
  `gh pr edit` cannot make an open PR a draft or change its base. Use
  `gh pr ready --undo` or the GitHub page of the PR for that.
- `--auto` and `--skip-approval` still show the full title and description,
  and the quality gates still run.
- `--skip-approval` is for a caller that decided the release level before it
  started this skill. `/sdlc:ship` uses it.
- `--label` and `--update` are legacy flags. The skill ignores them.

## Create or update

There is no flag for this choice. When the branch has an open PR, the skill
updates its title and description. If not, the skill creates a new PR.

The skill does not know which case applies before the publish, so the publish
question is the same for both. The result line tells you:
`Pull request created: <url>` or `Pull request updated: <url>`.

## The release decision

A merged PR with no release label skips the release pipeline. Thus the skill
does not publish before the release decision is clear.

| Situation | What the skill does |
|---|---|
| A caller gave the release level, for example `/sdlc:ship` | Asks no release question. It uses the level as given. |
| You run `/sdlc:pr` with no release level | Asks you to choose **Set release level** or **Skip release (acknowledged)**. |
| You run `/sdlc:pr --auto` with no release level | Stops with an error. No flag of `/sdlc:pr` gives a release level. Run `/sdlc:ship`. |
| The current commit already has a release tag | Records no release level, also when a caller gave one. This prevents a second bump. |

**Set release level.** What you see depends on your project:

- **The project has a version config.** The skill shows the counts of the
  conventional commits (breaking, feat, fix, other) and the level that they
  suggest. Then it shows one option for each level with its target version,
  for example `minor → 1.4.0 (preview)`. It can also offer a release
  candidate, for example `minor (RC) → 1.4.0-rc1 (preview)`.
- **The project has no version config.** The skill asks for the level
  (`major`, `minor` or `patch`) and asks if it is a release candidate. It
  shows no target version.

The version numbers are a preview. The PR records only the level and the
release candidate choice. CI computes the final number at merge time, so a
release that lands first can change it.

Then the skill drafts the release notes: some bullets that cover every commit
since the last tag, or every commit of the branch when no tag data is
available. You can change the draft.

**What the publish adds for a release level:**

- The label `release:<level>`, or `release:<level>-rc` for a release
  candidate. The publish removes each other `release:*` label of the PR.
- The release markers in the PR body, with the release notes below a
  `## [Unreleased]` heading. The PR holds no version number.
- One line before the description, for example
  `Release: patch (pre-release: rc) — publishing will apply label "release:patch-rc"`.

**Skip release (acknowledged).** The skill publishes the PR with no release
label and does not ask again in the same run.

[PR Labels and Markers](../../versioning.md#pr-labels-and-markers) explains
how CI reads the label and the markers.

## Stops

| Stop | What you see | What to do |
|---|---|---|
| Plan mode is active | A message that the skill needs write operations. | Exit plan mode, then run the skill again. |
| The preflight check fails | Each error. | Correct the cause, then run the skill again. |
| `--auto` with no release level | An error that names `/sdlc:ship`. | Run `/sdlc:ship`. |
| A link fails the link check | Each failed link with its `url`, `line`, `reason` and `detail`. | Decide how to correct the link. The skill changes no URL without your input. See [Link check](./description.md#link-check). |
| The publish fails | The error. The skill offers the drafted title and description. | Correct the cause, or create the PR by hand with the drafted text. |
| You answer `cancel` | Nothing more. | Run the skill again when you are ready. |

The preflight check fails for one of these causes:

- The config migration failed.
- The `gh` command is not signed in.
- The active `gh` account is not the account that `.sdlc-v2/local.toml`
  expects.
- A personal key in `.sdlc-v2/config.toml` could not move to
  `.sdlc-v2/local.toml`.
- The branch guard failed.
- The current branch is the default branch (`main` or `master`).

A warning does not stop the run. One example is the warning about uncommitted
changes: those files are not part of the PR.

## Limits

- **The skill needs the `gh` command, signed in.** Run `gh auth login` first.
- **The skill runs on a feature branch only.** It stops on `main` or `master`.
- **The skill adds no label of its own.** The only label is the `release:*`
  label. Use `gh pr edit --add-label` for each other label.
- **The skill does not try again after a `gh` failure.** It does not change
  the `gh` account. It shows the error and stops.
- **A stopped run has no resume.** Run `/sdlc:pr` again from the start.
