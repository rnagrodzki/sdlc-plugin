# Flags and limits

Read this page when you want to know each flag of `/sdlc:commit`, which files
go into the commit, or what the skill never does. The short overview is
[/sdlc:commit](../commit.md).

## Example

```text
/sdlc:commit --type fix --scope auth --auto
```

The message generator gets `fix` as the type hint and `auth` as the scope
hint. The skill shows the commit plan and commits with no question.

## Flags

| Flag | What it does |
|---|---|
| `--type <type>` | Gives a hint for the commit type to the message generator. |
| `--scope <scope>` | Gives a hint for the commit scope to the message generator. |
| `--auto` | Skips the `yes`, `edit` or `cancel` question. The skill still shows the commit plan. |
| `--force-default-branch` | Deprecated. It has no effect. |

- No tool checks a hint. The skill drops a hint, and tells you, when
  `commit.allowedTypes` (for `--type`) or `commit.allowedScopes` (for
  `--scope`) is set and does not contain the value. The commit rules win over
  the flags. See [Message rules](./message-rules.md#commit-rules-in-the-project-config).
- `--no-stash` and `--amend` are legacy flags. The skill ignores them: it
  never stashes and never amends.

## What goes into the commit

| Files | In the commit? |
|---|---|
| The files that you staged | Yes. At least one file must be staged. If not, the run stops with `no files staged for commit`. |
| Tracked files with changes that you did not stage (modified or deleted) | Yes. The plan lists them under `Also staged by commit_apply:`. |
| Tracked files under `.sdlc-v2/` that you did not stage | No. The plan lists them under `Left out (.sdlc-v2/):`. |
| Untracked files | No. The plan lists them under `Not committed (untracked):`. Run `git add` on a new file first. |

Staging does not narrow the commit. The message describes the staged diff
only. When the other tracked files change what the commit means, answer
`edit` and correct the message.

## The commit plan

The skill shows this plan before it asks `Commit as shown?`. The layout comes
from the `SKILL.md` of the skill, and the values are sample values:

```text
Commit
────────────────────────────────────────────
Message:    feat(auth): add OAuth2 PKCE flow

            Replaces the implicit flow with PKCE to comply with
            the new OAuth 2.1 requirements.

Staged:     3 files changed, +142, -12
  src/auth/pkce.ts
  src/auth/index.ts
  tests/auth/pkce.test.ts

Also staged by commit_apply:
  src/auth/session.ts

Left out (.sdlc-v2/):
  .sdlc-v2/config.toml

Not committed (untracked):
  src/auth/notes.md

Trailer:    OpenSpec-Change: add-oauth2-pkce

Note:       the message describes the staged diff only; the "also staged"
            files are committed with it. No stash-isolation in this port.
────────────────────────────────────────────
```

The skill leaves out the `Also staged`, `Left out` and `Not committed` blocks
when they are empty. The `Trailer:` line shows only when the
[OpenSpec trailer](./message-rules.md#openspec-trailer) applies.

Your answers:

- **yes**: the skill runs the [two gates](./message-rules.md#gates-before-the-commit), then commits.
- **edit**: you say what to change. The skill changes the message and shows the plan again.
- **cancel**: the skill stops and changes nothing.

## After the commit

The skill shows the first 7 characters of the commit SHA, the subject and the
result summary of `commit_apply`. It names each path that it did not commit:
the untracked paths and the tracked paths under `.sdlc-v2/`.

A named path is still uncommitted. Run `git add <paths>` and `/sdlc:commit`
again. This makes a second commit, because the first commit is done.

## Warnings and notices

- **Default branch.** When you are on the default branch, the skill warns you
  before the question. Nothing blocks the commit: the question is the only
  gate, and `--auto` skips the question.
- **WIP commits.** The skill reports the commits of your branch with a subject
  that starts with `wip(` or `wip:`. It does not squash them. They stay as
  separate commits, and the new commit is one more commit. Squash them
  yourself first when you want one commit for the change.
- **Plan mode.** The skill stops in plan mode, because it writes to git. Exit
  plan mode, then run `/sdlc:commit` again.

## Errors

| Cause | What the skill does |
|---|---|
| No file is staged | Shows `no files staged for commit` and stops. Stage a file, then run the skill again. |
| `commit_prepare` fails, or it reports a config-check error | Shows the error and stops. |
| The manifest file for the message generator was not written | Shows the `manifestPath:` warning and stops. |
| The subject does not match `commit.subjectPattern` | Does not commit. See [Subject pattern gate](./message-rules.md#subject-pattern-gate). |
| A link in the message body fails the check | Shows the list of violations and stops. It does not try again. Correct the links. See [Link gate](./message-rules.md#link-gate). |
| A pre-commit hook rejects the commit | Tells you to correct the hook problem and to run the skill again. No stash exists, so nothing needs a restore. |
| `commit_apply` fails for another cause | Shows the error. When the error looks like a defect of the plugin, the skill proposes an issue through [/sdlc:error-report](../error-report.md). |

## Limits

- **No amend.** To change the last commit, use `git commit --amend` directly,
  or reset and commit again.
- **No partial commit.** Tracked changes and staged files go into one commit.
  To commit a part of your working tree, move the other edits out first with
  `git stash push -- <paths>`, or commit by hand with plain `git commit`.
- **No untracked files.** Run `git add` on a new file before you run the
  skill, or the file stays out.
- **No `.sdlc-v2/` changes.** The skill leaves changes under `.sdlc-v2/` in the
  working tree. Commit them yourself when you want them in the history.
- **No WIP squash.** WIP commits stay as separate commits.
- **No resume.** A stopped run does not continue. Run `/sdlc:commit` again.
