# Issue and manual command

Read this page when you run `/sdlc:error-report` by hand, or when you want to know what the issue contains before you approve it. For the value of the skill and its steps, see the [overview](../error-report.md).

```text
/sdlc:error-report skill=<skill> step=<step> operation=<operation> error=<errorText> exitOrHttpCode=<code> errorType=<type>
```

A calling skill gives you this line when it cannot start `/sdlc:error-report` itself. In the normal flow you do not type the line.

## Fields

| Field | Description | Required |
|---|---|---|
| `skill` | The skill that hit the error. It becomes the second label of the issue. | yes |
| `step` | The step or operation that failed. | yes |
| `operation` | What the skill tried to do. | yes |
| `error` | The full error: exit code, message and HTTP status. | yes |
| `exitOrHttpCode` | The exit code or the HTTP status. | no |
| `errorType` | The kind of error, such as script crash, CLI failure or persistent API error. | no |

A calling skill can also send what you were doing, the arguments of its command, and hints for the investigation.

## Target, title and labels

| Part | Value |
|---|---|
| Repository | `rnagrodzki/sdlc-plugin`, always. The repository of your project is not the target. |
| Title | `[<skill>] <one-line error summary>`, 72 characters or fewer. |
| Labels | `tooling-error` and the name of the calling skill. |

## Body

The body comes from the `ToolingError.md` template. It has these sections:

| Section | Content |
|---|---|
| Error Summary | What failed, in one line. |
| Skill Context | The skill, the step, the operation and a timestamp. |
| Error Details | The error type, the exit code or HTTP status, and the full error text in a code block. |
| Environment | The repository from the git remote, and the current branch. |
| Reproduction | What you were doing, the command of the skill with its arguments, and the step that failed. |
| Impact | What the error blocked, in one line. |
| Suggested Investigation | The hints of the calling skill. |

The skill removes a section that has no data. The body has no `{placeholder}` text and no AI attribution line.

You see the full title and body at the second gate. Answer `edit` to change them before the skill files the issue.

## Comment on a similar issue

The skill searches for similar issues one time, after the first `yes`. A similar issue is open, has the labels `tooling-error` and the skill name, and has a title that starts with `[<skill>]`.

When the search finds issues, you choose one of 3 answers after you approve the draft:

| Answer | Result |
|---|---|
| `comment` | You pick an issue. The skill shows the comment and asks `Post this comment? (yes / cancel)`. After `yes` it prints `Comment added to #<number> — <url>`. |
| `new` | The skill creates a new issue and ignores the matches. |
| `cancel` | The skill creates nothing. |

The comment starts with the line `Additional occurrence reported by error-report:`. The body of the draft follows.

When the search finds no issue, the skill asks no question and creates the issue. When the search did not run, the skill prints a note that says why, then creates the issue.

## Failures

| Situation | What the skill does |
|---|---|
| `gh issue create` fails | Creates the labels that are missing, then tries one more time. |
| The second try fails | Prints `Could not create GitHub issue: <error>` and returns to the calling skill. It does not try again. |
| `prepare_orchestrator` returns an error | Shows the error and stops. It does not report its own failure. |
| The skill cannot read the draft of the agent | Stops. It creates nothing. |

The skill never replaces the error output of the calling skill.
