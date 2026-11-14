# Message rules

Read this page when you want to set commit rules for your project, or when
`/sdlc:commit` stops because a message breaks a rule. The short overview is
[/sdlc:commit](../commit.md).

## Example

Set the commit rules of your project with the setup skill:

```text
/sdlc:setup --only commit
```

[/sdlc:setup](../setup.md) configures the `commit` section, which holds the
commit message style. The project config is `.sdlc-v2/config.toml`. You can
also edit that file directly. From then on, `/sdlc:commit` applies the rules
to each message.

## How the skill finds your style

The message generator reads your recent commits and looks for these patterns:

- Conventional commits: `type(scope): description`
- Plain imperative English
- A ticket prefix, for example `PROJ-123: ...`
- Capitalization conventions

A new repository with no recent commits gets conventional commits as the
default style. The commit rules of the project config win over the style of
the recent commits.

## Commit rules in the project config

The skill reads the `commit` rules of `.sdlc-v2/config.toml`:

| Key | Rule |
|---|---|
| `commit.subjectPattern` | The regular expression that the subject must match. |
| `commit.subjectPatternError` | Your error message for a subject that does not match the pattern. |
| `commit.allowedTypes` | The allowed commit types, for example `["feat", "fix", "refactor"]`. |
| `commit.allowedScopes` | The allowed scopes. Optional. |
| `commit.requireBodyFor` | The types that need a commit body, for example `["feat", "breaking"]`. |
| `commit.requiredTrailers` | The trailers that each commit body must contain, for example `["Reviewed-By", "Closes"]`. |

The message generator treats each rule as mandatory. When the project has no
`commit` section, the skill skips each of these rules.

## Checks on each message

The message generator checks its message against this list before the skill
shows it to you:

| Check | Pass condition |
|---|---|
| Style match | The message agrees with the patterns of the recent commits. |
| Subject length | The subject has 72 characters or fewer. |
| Accuracy | Each claim comes from the staged diff. |
| Type correctness | The type matches the change: `feat` is a new feature, `fix` is a bug fix, `refactor` is a restructure, `chore` is maintenance. |
| Imperative mood | The subject says "add", not "adds" or "added". |
| No fabrication | The message invents nothing beyond the staged changes. |
| Body relevance | The body adds value or is absent. It does not repeat the subject. |
| No WIP prefix | The subject does not start with `wip:` or `wip(`. |
| Pattern match | The subject matches `commit.subjectPattern`, when it is set. |
| Required body | The body is present when the type is in `commit.requireBodyFor`, when it is set. |
| Required trailers | Each key of `commit.requiredTrailers` is in the body, when it is set. |

More habits of the message:

- A body is optional. The message has a body only when the change is not
  trivial and the reason adds value.
- The subject says what changed. The body says why.
- The subject holds no file path.

## Gates before the commit

After your `yes`, or directly with `--auto`, the skill runs two gates. A
failed gate means no commit.

### Subject pattern gate

The skill checks the subject against `commit.subjectPattern`, when it is set.
On a mismatch it shows `commit.subjectPatternError`, or the pattern when no
error message is set. Then it offers these choices:

- **edit subject**: you correct the subject, and the skill checks it again.
- **harden**: the skill runs [/sdlc:harden](../harden.md) to find why the
  subject failed and to propose stronger guardrails. Harden edits nothing
  without your approval. This choice does not show with `--auto`.
- **cancel**: the skill stops with no commit.

No other choice passes this gate.

### Link gate

The skill checks each link in the message body. A link that fails the check
is a violation. The skill shows the list of violations with the URL, the
line, the reason and the detail of each one, then it stops. It does not try
again, and it does not change a URL without your input.

## OpenSpec trailer

The skill adds the trailer `OpenSpec-Change: <change-directory-name>` to the
message when all of these conditions are true:

- The repository has `openspec/config.yaml`.
- You gave no `--scope`.
- Exactly one active change exists, or one change matches the name of the
  current branch.
- The message has a body.

## Learnings

After a commit in a project with a commit style that is not conventional or
is unusual, the skill adds a note about the style to the learnings log.
