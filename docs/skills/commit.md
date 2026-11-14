# /sdlc:commit

Commit your work with a message in the style of your project, after you see the full commit plan.

## Why use it

You have changes ready, and the message must follow the commit style and the commit rules of your project.
A message that breaks a rule, or a file that is missing from the commit, costs you a second commit.
`/sdlc:commit` writes the message from your staged diff and your recent commits, and shows the message with the files.
It commits after you answer `yes`.

## Example

Stage at least one file with `git add <files>`, then run:

```text
/sdlc:commit
```

The skill shows the commit plan and asks `Commit as shown?`. The plan has the message, then these file lists:

- `Staged:` the files that you staged.
- `Also staged by commit_apply:` the other tracked files that have changes. They go into the commit too.
- `Left out (.sdlc-v2/):` and `Not committed (untracked):` the files that stay out of the commit.

Answer `yes`, `edit` or `cancel`. After `yes`, the skill shows the short SHA of the new commit and names each path that it did not commit.

## What you get

- **A message in the style of your project.** The skill finds the style in your recent commits and writes the message from the staged diff only.
- **Commit rules applied for you.** The message follows the subject pattern, the allowed types and scopes, the required body and the required trailers of your project config.
- **No surprise in the commit.** You see the files that go in and the files that stay out before you answer.
- **Control of each commit.** The skill commits nothing before your `yes`. Only `--auto` skips the question.
- **Bad links stopped before the commit.** The skill checks the links in the message body and stops when a link fails the check.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Plan Mode Check | skill | Stops the run when plan mode is active, because the skill writes to git. | The go-ahead for the next step | no | always. It stops the run only in plan mode |
| Step 0 (CONSUME): Call `commit_prepare` | tool | Calls `commit_prepare`. Shows each error and stops, or shows each warning and continues. | `COMMIT_CONTEXT` and the manifest file | no | always |
| Step 0.5 (BRANCH-GUARD): not functional in this port | skill | Does nothing. `branchGuard` is always `ok` in this port, so the skill goes to Step 1. | Nothing | yes | never. The branch guard is not functional in this port |
| Step 1 (CONSUME): Quick Context Read | skill | Reads the branch, the staged, unstaged and untracked files, the recent commits and `commitConfig` from `COMMIT_CONTEXT`. | The facts for the commit plan | no | always |
| Step 1c (WIP-commit squash detection — reporting only) | skill | Reports the WIP commits (subjects that start with `wip(` or `wip:`). It does not squash them. | A WIP notice | yes | `wipSquash.commits` is not empty |
| Step 2 (PLAN): Dispatch the commit-orchestrator Agent | agent | Dispatches `sdlc:commit-orchestrator` with the manifest path. The agent drafts the message and applies every `commitConfig` rule. | `MESSAGE` | no | always |
| Step 3 (CRITIQUE) and Step 4 (IMPROVE) | agent | The orchestrator agent runs the critique and the improvement inside Step 2. The main session does not repeat them. | The critiqued `MESSAGE` | no | always |
| Step 5 (DO): Present and Execute | skill | Shows the commit plan and asks `yes`, `edit` or `cancel`. On `yes` it checks the subject pattern and the links, then calls `commit_apply`. | The new commit and its `sha` | no | always. `--auto` skips the question |
| Step 6 (CRITIQUE): Verify | skill | Reads `sha`, `summary` and the skipped paths. Shows the result and names each path that was not committed. | The commit report | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:commit`. At least one file must be staged. |
| Ends | After Step 6 shows the commit result. A `cancel`, an error or a rejected commit hook ends the run with no commit. |
| Asks the user | At Step 5: `yes`, `edit` or `cancel`. When the subject does not match `commitConfig.subjectPattern`: `edit subject`, `harden` or `cancel`. `--auto` skips the first question only. |
| Resume | none |

## More examples

**Commit with no question:**

```text
/sdlc:commit --auto
```

The skill shows the commit plan, then commits with no question. Use it in an unattended workflow, for example with `/sdlc:ship`.

**Give a hint for the type and the scope:**

```text
/sdlc:commit --type fix --scope auth
```

The message generator gets `fix` as the type hint and `auth` as the scope hint. The skill drops a hint that your commit rules do not allow, and tells you.

**Commit only a part of your changes:**

```text
git stash push -- <paths>
```

Staging a subset does not narrow the commit: each tracked file with changes goes in. Move the edits that must stay out with this command, then run `/sdlc:commit`.

## Reference

- [Flags and limits](./commit/flags-and-limits.md): the 4 flags, the files that go into the commit, the commit plan, the errors and the limits.
- [Message rules](./commit/message-rules.md): the style detection, the commit rules of the project config, the checks on each message and the two gates before the commit.

## Related skills

- [/sdlc:setup](./setup.md): sets the commit rules of your project.
- [/sdlc:execute](./execute.md): makes the WIP commits that this skill reports.
- [/sdlc:review](./review.md): reviews the changes after the commit.
- [/sdlc:pr](./pr.md): opens a pull request after the commit.
- [/sdlc:ship](./ship.md): runs execute, commit, review and the pull request as one pipeline.
