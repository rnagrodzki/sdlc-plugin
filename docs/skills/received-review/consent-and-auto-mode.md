# Flags, consent and auto mode

Read this page when you run `/sdlc:received-review` with a flag, when you want
fewer prompts, or when a pipeline runs the skill with `--auto`.

```text
/sdlc:received-review [--pr <number>] [--auto] [--no-harden]
```

The [overview page](../received-review.md#example) shows an example.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--pr <number>` | The pull request whose review comments the skill reads and answers. | none. With no PR number, the skill takes the feedback from the dispatch prompt, the conversation or your paste, and posts no reply. |
| `--auto` | Run without prompts. Only "agree, will fix" ends a finding. Every other outcome is recorded as a `needs-direction` follow-up and is not closed. See [Auto mode](#auto-mode). | off |
| `--no-harden` | Skip the harden dispatch in Step 11.6. Use it when a caller already owns hardening for the run, for example the `harden` step of `/sdlc:ship`. See [The harden dispatch](#the-harden-dispatch). | off |

## Prompts of a manual run

A run without `--auto` is a manual run. It changes no code before you answer
`implement`, except for the findings that you pre-approved by severity. It
posts no reply before you answer the reply prompt.

| Step | The skill asks | Your answer |
|---|---|---|
| Step 2 | About all unclear comments at once. The run stops until you answer, because the comments can be related. | Your clarification |
| Step 3 | For direction on each comment that it cannot verify, for example a comment that needs runtime data. | Your direction |
| Step 10 | How to proceed with the plan. | `implement` applies the fixes. `edit` changes the plan and shows it again. `skip` discards the prompted findings. |
| Step 11.6 | To run `/sdlc:harden` for a group of findings on one file. One prompt for each group. | `dispatch` or `skip` |
| Step 12 | To post the replies on the pull request. | `yes` posts the replies. `skip` posts none. `selective` lets you choose the threads. |

The Step 10 prompt also applies in a pipeline. Only `--auto` in the arguments
of this run, or a pre-approved severity, removes it. The skill does not infer
an unattended run from its caller.

## Consent and auto-apply severities

Set `receivedReview.alwaysFixSeverities` in `.sdlc-v2/local.toml` (or `~/.sdlc/local.toml`).
Allowed values: `critical`, `high`, `medium`, `low`, `info`. Default: `[]` (every finding asks).

```toml
[receivedReview]
alwaysFixSeverities = ["critical", "high"]
```

| Situation | What happens |
|---|---|
| `--auto` | Every "will fix" finding gets its fix. The setting adds nothing. |
| A "will fix" finding with a listed severity | It gets its fix with no prompt. One `pre-approved` line shows it, for example `pre-approved (high in alwaysFixSeverities): internal/api/user.go:42 — <title>`. |
| A comment with no severity | It counts as `medium`. |
| Other findings exist | One prompt asks about them only. |
| You answer skip | Only the pre-approved findings get their fix. Skipped findings get no fix, no reply and no record. |
| You answer edit | The skill shows the plan again and computes the pre-approved findings again. |
| Every finding is pre-approved | No Step 10 plan prompt. The later prompts still ask in manual mode: the Step 11.6 harden prompt and, when a PR exists, the Step 12 reply prompt. |
| No finding needs an action | No prompt. Only the summary shows. |
| A bad value: a wrong type (including a `receivedReview` value that is not a table) or an unknown item such as `"blocker"` | The whole list becomes `[]`, one warning shows, and every finding asks. `["high", "blocker"]` pre-approves nothing. |
| The local file cannot be read | The error shows, and every finding asks. |

The setting is not auto mode. It removes the Step 10 prompt only for "will fix"
findings with a listed severity. Pushback, "will not fix" and "needs direction"
findings still ask.

## Auto mode

`--auto` is for pipeline and subagent dispatch, where there is no person to
ask. It does not mean "agree with everything". It allows one verdict to end a
finding:

- **`agree, will fix`** ends a finding. The skill makes the fix, and the review
  thread gets a reply that says so.
- **Every other outcome becomes `needs-direction`.** The skill records the
  finding and does not close it. This applies to "agree, won't fix",
  "disagree", "cannot verify" and a comment that is too unclear to evaluate.
  The thread gets a reply that names the open question, and the finding goes
  to the backlog of [/sdlc:deferred](../deferred.md).

An unclear comment stops a manual run with a question. Under `--auto` the
skill records the comment, with a note on what is unclear, and continues with
the other comments.

**A fix that fails its check is reverted.** If a change does not compile or
does not pass its tests, the skill undoes only that change. It records the
finding as `needs-direction` with the failure in the description. It does not
try a second approach with no person present. This rule applies in manual mode
too, where the summary also names the failed fix.

**`needs-direction` needs a real choice.** The record names two or more
candidate approaches and a one-line trade-off between them. One obvious
approach is not a question: the skill makes the fix. The exceptions are
records with no approach to weigh, such as an unclear comment or a failed fix.
These records say what happened.

**No pull request, no reply step.** `/sdlc:ship` can dispatch this skill in its
fix loop before a pull request exists. Then there are no review threads. The
skill skips the reply step and the reply check, and shows only the summary and
the ledger.

[Records and the ledger](./records-and-ledger.md) describes where the records
go and how the ledger counts them.

## The harden dispatch

Step 11.6 groups the findings by file, with at most 5 groups, and runs
[/sdlc:harden](../harden.md) for each group that is not hardened yet. A group
is hardened when the live `/sdlc:ship` run already holds a harden record with
the same failure text; the skill skips that group. The step is best-effort: a
failure does not stop the replies.

- **Manual run.** The skill asks `dispatch` or `skip` for each group that is
  not hardened yet.
- **`--auto`.** The skill asks nothing and passes `--auto` to each
  `/sdlc:harden` run. `/sdlc:harden` then applies guardrail edits and
  review-dimension edits to your project with no prompt: add, strengthen,
  consolidate, repair, and the removal of a rule with a dead reference. Merges and other removals
  wait as pending deferred items, as
  [Auto mode and pending edits](../harden/auto-mode-and-edits.md#auto-mode-and-pending-edits)
  describes. The summary of this skill lists each applied edit under
  "Auto-accepted" and each pending edit under "Pending".
- **`--no-harden`.** The skill skips the step. Use the flag when the
  [`harden` step of `/sdlc:ship`](../ship/pipeline-steps.md#harden) already
  owns hardening for the run.
- **A subagent with no way to ask.** When the skill cannot ask you and `--auto`
  was not passed, it skips the step and says so in the summary. It never adds
  `--auto` on its own.
- **Groups that get no run.** The summary lists each suppressed group and each
  lone `disagree` finding that formed no group, as the grouping returned them.

## Tips and gotchas

- **Dual self-critique.** The skill verifies each comment against the actual
  code before it accepts the comment. If a comment is incorrect, the reply
  explains why. The skill checks its evaluation and its replies one more time
  before you see them.
- **Not only for human reviewers.** The skill processes feedback from any
  source: a person, Copilot, or the findings of `/sdlc:review`.
- **Fix loop in `/sdlc:ship`.** Inside `/sdlc:ship`, the skill runs only when
  `/sdlc:review` produced a finding at or above the review threshold. Findings
  below the threshold are deferred, not dropped. See
  [The received-review step](../ship/pipeline-steps.md#the-received-review-step).
- **Threads stay open.** The skill replies to a review thread and never
  resolves it. Resolve the thread in the GitHub UI when the reply satisfies
  you.
- **A link violation stops the replies.** Before the skill posts, it checks
  every URL in the reply bodies. On a violation it shows the list, posts no
  reply and stops.
- **Plan mode.** The skill edits files and posts replies. In plan mode it
  stops at once: exit plan mode, then run it again.
- **Contradictory comments.** When two threads of one reviewer contradict, the
  skill flags the contradiction and asks. It does not guess.
- **Comments on deleted lines.** A comment can point to code that is gone. The
  skill verifies it against the current HEAD, not against the diff in the
  review.
- **`gh` access.** When `gh` cannot read the comments because of an auth or
  access error, the skill shows the error and stops. Check `gh auth status`
  and the PR number.
