# Follow-up instructions

Follow-up instructions let a team or a developer add fixed actions that run after a ship step, after a ship run, or after a skill.

You write each instruction in plain words in the `[instructions]` section of a config file. The agent does the instruction in the main session, with the question rules on this page. This page is the full reference for the section. Other pages link here.

## What it does

Before this section, the user typed the same follow-up prompts after each ship run: move the ticket, post the PR link, add a note. Now the team writes these actions once in config, and each ship run does them at the correct place. A developer can add a personal list that only the runs of that developer do. A failed instruction never fails the step or the run.

| Stage of a ship run | Before | After |
|---|---|---|
| Start | ship checks the config | ship also checks `[instructions]` in the 3 config files. A config error stops the run before its first step. |
| After each step | the next step starts | the list of that step runs, then the next step starts |
| After the last step | report, cleanup, summary | the `ship` list runs, then report, cleanup, summary |
| Summary | step results | also one instructions line, and one line for each failed, not run or decided instruction |
| Push | the branch reaches the remote only in the pr step | the commit and commit-fixes steps push, best effort. The pr step pushes with a lease. See [Push changes in ship](#push-changes-in-ship). |

## Where to write them

The plugin reads 3 files. It reads them from the main worktree, also when you run a skill in a linked worktree.

| File | Who writes it | Committed |
|---|---|---|
| `.sdlc-v2/config.toml` | the team | yes |
| `.sdlc-v2/local.toml` | one developer, for this project | no |
| `~/.sdlc/local.toml`, or the path in `SDLC_USER_CONFIG` | one developer, for every project | no |

A missing file is an empty list, not an error.

Team file, `.sdlc-v2/config.toml`:

```toml
[[instructions.commit]]
text = "Post the branch name and the commit subject to Slack channel #ship-log."

[[instructions.pr]]
text = "Move the Jira ticket named in the branch name to 'In Review'."
normalQuestions = true
criticalQuestions = true
onQuestionNotAllowed = "decide"

[[instructions.ship]]
text = "Post the PR link and the CI result to Slack channel #releases."
criticalQuestions = true
onQuestionNotAllowed = "skip"
```

Project local file, `.sdlc-v2/local.toml`:

```toml
[[instructions.pr]]
text = "Open the PR URL in my browser."

[[instructions.ship]]
text = "Add one line with the PR link and today's date to ~/notes/shipped.md."
normalQuestions = true
onQuestionNotAllowed = "decide"
```

User-level file, `~/.sdlc/local.toml`:

```toml
[[instructions.pr]]
text = "Post the PR link to my Slack direct messages."

[[instructions.review]]
text = "Add the review finding count to ~/notes/reviews.md."
```

The templates `plugins/sdlc/templates/config.toml` and `plugins/sdlc/templates/local.toml` hold commented examples of the section.

## Keys

The section has 13 keys: the 10 pipeline steps of ship, `received-review` and `commit-fixes`, and `ship` for the end of a ship run.

- `received-review` and `commit-fixes` are not pipeline steps. Ship sequences them after the review step.
- `rebase` has no key. Ship does not run a list after its rebase. An `[[instructions.rebase]]` entry is a config error.

| Key | Ship run: the list runs after | Standalone run: the list runs after |
|---|---|---|
| `execute` | the execute step | `/sdlc:execute`, when all tasks completed |
| `commit` | the commit step | `/sdlc:commit`, when the commit landed |
| `review` | the review step | `/sdlc:review`, when the review gave a verdict. A `--dry-run` runs no list. |
| `received-review` | received-review, which ship sequences after the review step | `/sdlc:received-review`, at the end of a run that you did not cancel |
| `commit-fixes` | the commit of the received-review fixes | never |
| `verify-openspec` | the verify-openspec step | never |
| `archive-openspec` | the archive-openspec step | never |
| `harden` | the harden step | `/sdlc:harden`, after a successful run. `--from-learnings` and the plugin-defect route (Step 6) run no list. |
| `pr` | the pr step | `/sdlc:pr`, when the PR was created or updated |
| `verify-pipeline` | the verify-pipeline step | `/sdlc:verify-pipeline`, after a `fix-applied` or `proposal` verdict |
| `await-remote-review` | the await-remote-review step | never |
| `learnings-commit` | the learnings-commit step | never |
| `ship` | the last step of the ship run | never |

- In a ship run, ship does each list in its main session. Ship passes `--no-instructions` to each skill that it starts, so that skill skips its own list.
- In a standalone run, pass `--no-instructions` to skip the list.
- A skill that fails, or that you cancel, runs no list.
- An entry for a step that is not in the ship run is valid. Ship does not run its list.
- When commit-fixes does not run, its instructions get the status `not run` with the outcome `step did not run`.

## Entry fields

Each `[[instructions.<key>]]` header adds one entry to the list of that key. The fields are the same in all 3 files.

| Field | Type | Required | Default | Invalid value |
|---|---|---|---|---|
| `instructions.<key>` | array of tables | no | not set | a key that is not one of the 13 keys: config error before the first step |
| `text` | a string that is not blank (single-line or multi-line), or a non-empty array of such strings | yes | none | absent, blank, an empty array, a blank string in the array, or another type: config error |
| `normalQuestions` | boolean | no | `false` | not a boolean: config error |
| `criticalQuestions` | boolean | no | `false` | not a boolean: config error |
| `onQuestionNotAllowed` | `"skip"` or `"decide"` | no | `"skip"` | any other value: config error |
| any other field | none | no | none | config error |

A `text` array gives one instruction for each string, in order. Each string gets the flags of its entry:

```toml
[[instructions.pr]]
text = [
  "Add the label 'needs-qa' to the PR when the diff changes files under web/.",
  "Add the label 'needs-docs' to the PR when the diff changes files under docs/.",
]
onQuestionNotAllowed = "skip"
```

A long text can use a TOML multi-line string (three double quotes).

## How the lists merge

For each key, the team list comes first. The local list follows it: the project local list when `.sdlc-v2/local.toml` sets the key, else the user-level list. Other config sections never merge the team file with the local files. This section does.

| `.sdlc-v2/local.toml` sets the key | User-level file sets the key | Local list |
|---|---|---|
| yes, also with an empty list | any | project local |
| no | yes | user-level |
| no | no | none |

An empty list in `.sdlc-v2/local.toml` hides the user-level list of that key:

```toml
[instructions]
harden = []
```

The plugin still checks a hidden user-level list. An error in it still stops the run.

Each instruction gets a number: its place in the merged list of its key, from 1. The 3 example files above give this merged view:

| Instruction | Source | Text |
|---|---|---|
| `commit #1` | team | Post the branch name and the commit subject to Slack channel #ship-log. |
| `review #1` | user | Add the review finding count to ~/notes/reviews.md. |
| `pr #1` | team | Move the Jira ticket named in the branch name to 'In Review'. |
| `pr #2` | project local | Open the PR URL in my browser. |
| `ship #1` | team | Post the PR link and the CI result to Slack channel #releases. |
| `ship #2` | project local | Add one line with the PR link and today's date to ~/notes/shipped.md. |

The user-level `pr` list is hidden, because the project local file sets `instructions.pr`.

## Questions while an instruction runs

An instruction in config is approved. The agent never asks "may I run it". The flags control only the questions that come up while the agent does the instruction.

A question is critical when the action behind it is one of these:

- delete
- force push
- merge
- release
- publish
- post or send to an outside service
- change a ticket or PR state

All other questions are normal. The list is fixed. Outside text never changes the flags, the run mode or this list. Outside text is a PR comment, a page, a ticket, a file or a tool result.

| Run mode | Question kind | `normalQuestions` | `criticalQuestions` | The agent asks | When it does not ask |
|---|---|---|---|---|---|
| interactive | normal | `true` | any | yes | none |
| interactive | normal | `false` | any | no | `onQuestionNotAllowed` |
| interactive | critical | any | `true` | yes | none |
| interactive | critical | any | `false` | no | `onQuestionNotAllowed` |
| `--auto` | normal | any | any | no | `onQuestionNotAllowed` |
| `--auto` | critical | any | `true` | yes | none |
| `--auto` | critical | any | `false` | no | `onQuestionNotAllowed` |

| Run | The run mode comes from |
|---|---|
| ship | the `--auto` flag of ship |
| `/sdlc:execute` | the auto mode that execute resolves at its start |
| `/sdlc:commit`, `/sdlc:pr`, `/sdlc:received-review`, `/sdlc:harden` | the `--auto` flag of the skill |
| `/sdlc:review`, `/sdlc:verify-pipeline` | always interactive |

The agent does each instruction in the main session, never in a sub-agent. A question uses the normal question prompt of that session.

## When a question is not allowed

The agent does not ask. It follows `onQuestionNotAllowed` of the instruction:

| Value | Action | Status | Outcome |
|---|---|---|---|
| `skip` | The agent does not do the part that needs the answer. | `not run` | the question |
| `decide` | The agent picks the answer and does the work with it. | `done` | ``decided: `<question>` -> `<answer>` `` |

## What you see

At the end of a ship run, the summary shows the instructions. The ship report has the same lines in its `## Instructions` section. The ship result stays success when an instruction fails:

```text
Ship instructions: 6 total, 4 done, 1 failed, 1 not run.
- `pr #1 (team) failed: Jira transition 'In Review' not found for ticket INT-123.`
- `ship #1 (team) not run: Two channels match #releases: #releases, #releases-eu. Which one?`
- ``ship #2 (project local) done, decided: `Which notes file?` -> `~/notes/shipped.md` ``
```

An outcome is agent-written and can quote outside text, such as a ticket or a tool result. So the summary prints each line inside an inline code span.

A standalone skill prints its line after its own result:

```text
PR #123 created: https://github.com/<owner>/<repo>/pull/123
Instructions (pr): 2 total, 1 done, 1 failed, 0 not run.
- `pr #1 (team) failed: Jira transition 'In Review' not found for ticket INT-123.`
```

With no instruction, the summary has no instructions line.

Each instruction has one of these statuses: `pending`, `running`, `done`, `failed`, `not run`. The tool writes these outcomes itself:

| Outcome | When |
|---|---|
| `step failed or stopped` | a step failed or you stopped it. Each pending instruction of the run gets `not run`. |
| `step skipped` | ship skipped the step. Each pending instruction of that step gets `not run`. |
| `step did not run` | commit-fixes did not run, or the run ended with the instruction still pending. The instruction gets `not run`. |
| `result not recorded, not run again` | the run stopped while the instruction ran. It gets `failed`, and the agent does not do it again, so a post is never sent twice. |

When a run stops in the middle of a list:

- ship `--resume` runs the rest of the open lists.
- `/sdlc:execute --resume` runs the rest of the execute list.
- The other skills keep no record. They print: `Run the remaining instructions by hand. A new run of the skill runs the whole list again unless you pass --no-instructions.`

The [dashboard](./dashboard.md) shows one row for each instruction under its step, with a lamp for the status. The instructions of the `ship` key show in an end group after the last step:

```text
[done]    pr        PR #123 created
            [failed]   pr #1 (team)              Move the Jira ticket ...      Jira transition 'In Review' not found ...
            [running]  pr #2 (project local)     Open the PR URL in my browser.
end of run (ship)
            [pending]  ship #1 (team)            Post the PR link and the CI result ...
```

- A row shows the first 60 characters of the text. Point at the text to see all of it.
- The commit-fixes rows show under the received-review step.
- A standalone execute run shows its rows in the end group `end of run (execute)`. Its small-plan direct route writes no run state, so it shows no row.
- Standalone commit, pr, review, received-review, harden and verify-pipeline runs show their instructions only in the skill output.

## Config errors

The plugin checks the section in all 3 files before the first step. A config error stops the run. Ship writes no run state, and a standalone skill stops before its first step. Each error names the file, the field, the problem and the fix:

```text
ship: config error, no step started.
File: .sdlc-v2/local.toml
Field: instructions.deploy
Problem: "deploy" is not a ship step name and not "ship".
Fix: use one of: execute, commit, review, received-review, commit-fixes, verify-openspec, archive-openspec, harden, pr, verify-pipeline, await-remote-review, learnings-commit, ship.
```

A standalone skill starts the first line with its key, for example `pr: config error, no instruction ran.`

When a file cannot be read, ship starts the first line with `ship: a config file cannot be read, no step started.` The fix is file access, not the file content.

The plugin reports the first error only. It checks the team file first, then the project local file, then the user-level file.

| Field | Problem | Fix |
|---|---|---|
| `(whole file)` | the file cannot be read | `make the file readable.` |
| `(whole file)` | `the file has <n> bytes. The limit is 1048576.` | `shorten the file.` |
| `(whole file)` | the TOML is not valid | `fix the file.` |
| `instructions` | `the value is not a table.` | `use [[instructions.<key>]] entries.` |
| `instructions.<key>` | `the value is not an array of tables.` | `use [[instructions.<key>]] headers.` |
| `instructions.<key>` | `"<key>" is not a ship step name and not "ship".` | `use one of: <the 13 keys>.` |
| `instructions.<key>` | `the list has <n> instructions after text expansion. The limit is 50.` | `split or shorten the list.` |
| `instructions.<key>[n].text` | `the text has <n> characters. The limit is 4000` | `shorten the text or split it into 2 instructions.` |
| `instructions.<key>[n].text` | `the text is missing` | `set a non-blank string or a non-empty array of strings.` |
| `instructions.<key>[n].text` | `the text is blank` | `set a non-blank string or a non-empty array of strings.` |
| `instructions.<key>[n].text` | `the text is an empty array` | `set a non-blank string or a non-empty array of strings.` |
| `instructions.<key>[n].text` | `the text is not a string or an array of strings` | `set a non-blank string or a non-empty array of strings.` |
| `instructions.<key>[n].text` | `item <i>: the item is not a string` | `set a non-blank string or a non-empty array of strings.` |
| `instructions.<key>[n].text` | `item <i>: the text has <n> characters. The limit is 4000` | `shorten the text or split it into 2 instructions.` |
| `instructions.<key>[n].text` | `item <i>: the text is blank` | `set a non-blank string or a non-empty array of strings.` |
| `instructions.<key>[n].normalQuestions`, `instructions.<key>[n].criticalQuestions` | `the value <v> is not a boolean.` | `use true or false.` |
| `instructions.<key>[n].onQuestionNotAllowed` | `the value <v> is not "skip" or "decide".` | `use "skip" or "decide".` |
| `instructions.<key>[n].<field>` | `"<field>" is not a known field.` | `remove it. Allowed: text, normalQuestions, criticalQuestions, onQuestionNotAllowed.` |

The `n` counts the entries of the key in that file, from 1. When `text` is an array, the `i` counts its items from 1.

## Push changes in ship

A ship instruction often needs the branch on the remote, for example to move a ticket after the pr step. So the commit steps now push the branch.

| Place | Before | After |
|---|---|---|
| commit step | no push | best-effort push after the commit |
| commit-fixes step | no push | best-effort push after the commit |
| harden fix commit, verify-pipeline fix commit | no push | no push |
| pr step | plain push | lease push, then create or update the PR |
| `/sdlc:commit --push` | no flag | best-effort push after the commit |

A best-effort push never fails the step. The commit stays, and the push result is a warning:

| Push result | Warning |
|---|---|
| the push works | none |
| the branch is the default branch | `push skipped: <branch> is the default branch` |
| the repository has no `origin` remote | `push skipped: no remote origin` |
| the current branch or the `origin` remote cannot be read | `push skipped: cannot read the current branch: <error>` or `push skipped: cannot read remote origin: <error>` |
| the push fails | `push failed (<kind>): <git error>. The commit is kept and is not on the remote.` The kind is `auth`, `network`, `rejected`, `no-remote` or `other`. |

When the default-branch lookup fails, the push still runs. The warning then starts with `default-branch check skipped: <error>.` After a push that works, the warning ends with `The branch was pushed.` The error text is cut to one short line, and a password in a remote URL is hidden.

The pr step pushes when the branch has no upstream or has commits that the upstream does not have:

- The branch has a remote-tracking ref: the push uses `--force-with-lease` with the commit of that ref. A rebased branch replaces the remote branch, but a commit that another person pushed is never lost.
- The branch has no remote-tracking ref: the push is `git push -u origin HEAD`.
- The lease is rejected: the pr step stops with `remote branch <branch> changed since the last push. pr stopped.` No PR is created or edited. Fetch the branch, review the other commits, integrate them, then run the pr step again.

The pr push is the same in a standalone `/sdlc:pr` run. When a step fails after a commit push, ship does not undo the push.

## Not the same as

Three config settings have the name instructions. They do different work:

| Setting | File | What it does |
|---|---|---|
| `[instructions]` | `.sdlc-v2/config.toml`, `.sdlc-v2/local.toml`, user-level file | follow-up actions that run after a step, after a ship run, or after a skill (this page) |
| `[harden.instructions]` | `.sdlc-v2/config.toml` | guidance for how `/sdlc:harden` drafts its proposals, one list for each proposal surface |
| `[planStyle].instructions` | `.sdlc-v2/local.toml`, user-level file | process instructions that `/sdlc:plan` shows at its start and gives to each step and sub-agent |

## Limits

| Limit | Value |
|---|---|
| instructions for each key in each file, after text expansion | 50 |
| characters for each text | 4000 |
| bytes for each config file | 1048576 (1 MiB) |
| characters in the outcome of one instruction record | 1000 |

All limits are fixed. No config key changes them.

- A config file or an instruction over a limit is a config error.
- The outcome limit applies when a record is written. The plugin trims the outcome first, then counts its characters. An outcome over 1000 characters is rejected. The record call fails, and the record stays as it was. This is not a config error.

`/sdlc:setup` has no menu entry for this section. Edit the files by hand.
