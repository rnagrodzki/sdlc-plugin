# Flags and verdicts

Read this page when you want to know each flag of `/sdlc:verify-pipeline`
or how to read its verdict line. The page also shows where the skill gets
the log and what it does for each failure category. The short overview is
[/sdlc:verify-pipeline](../verify-pipeline.md).

## Example

```text
/sdlc:verify-pipeline --pr 42 --auto
```

The skill gets the failure log of the first failed check on pull request 42.
For a `lint`, `test-failure` or `type-error` failure it edits the source
files, and the run ends with a `fix-applied` verdict line. See
[Verdicts](#verdicts) for the three lines.

## Flags

| Flag | What it does | Default |
|---|---|---|
| `--pr <number>` | Names the pull request whose failed CI check the skill analyzes. | none |
| `--logs <path-or-string>` | Gives the log. A file path makes the skill read the file. Any other value is the log text itself. | none |
| `--auto` | Lets the skill edit the source files for the three categories that it can fix. Without it, each run ends with a proposal or a stop. It does not change the run mode of the follow-up instructions. | off |
| `--no-instructions` | Do not run the follow-up instructions of the skill. Ship passes this flag, because ship runs the lists itself. See [Follow-up instructions](#follow-up-instructions). | off |

At least one of `--pr` and `--logs` is necessary. With neither flag, the run
stops with the reason `--pr or --logs required`.

## Where the log comes from

- With `--logs`, the skill uses the file contents or the text that you gave.
- With `--pr` and no `--logs`, the skill runs `gh pr checks <number>`. It
  takes the first row with a failing state: `fail`, `failure`, `cancelled`,
  `action_required` or `timed_out`. It reads the run ID from the
  `/actions/runs/<runId>` link of that row, then runs
  `gh run view <runId> --log-failed`.

The skill analyzes the first failing check only.

## Categories

The `verify_pipeline_classify` tool puts the log in 1 of 7 categories. It
also returns the signals: each pattern that matched the log.

| Category | With `--auto` | Without `--auto` |
|---|---|---|
| `lint`, `test-failure`, `type-error` | The skill edits the source files. Verdict: `fix-applied`. | Verdict: `proposal`. |
| `build-error`, `dependency`, `infra` | Verdict: `proposal`. These failures usually need the judgement of a person. | Verdict: `proposal`. |
| `unknown` | Verdict: `proposal`. The `summary` is the raw log excerpt. | Verdict: `proposal`. |

The edit is the minimal fix: it corrects the lint violation, fixes the
failing assertion, adds the missing import or corrects the type annotation.
The skill adds no abstraction and does no refactor.

## Verdicts

Each run prints exactly one of these JSON lines on stdout. Logs and
progress go to stderr. In a standalone run, the summary of the follow-up
instructions can follow the JSON line. Nothing comes before it.

```json
{"status":"fix-applied","filesChanged":["path/a","path/b"],"summary":"<one-line summary>"}
{"status":"proposal","summary":"<diagnosis>","suggestedPatch":"<diff-or-prose>"}
{"status":"abort","reason":"<reason>"}
```

| Status | Meaning | What comes next |
|---|---|---|
| `fix-applied` | The skill edited the files in `filesChanged`. It did not commit them. | You commit and push. In a ship run, ship commits the fix. |
| `proposal` | The skill changed no file. `summary` is the diagnosis. `suggestedPatch` is a diff or text. | You, or ship in a ship run, read the proposal and decide whether to apply it. |
| `abort` | The skill had no log to analyze. `reason` says why. | The run ends with no analysis. In a ship run, ship continues with a warning. |

### Stop reasons

| `reason` | Cause |
|---|---|
| `--pr or --logs required` | The command has no `--pr` and no `--logs`. |
| `no failed check found` | `gh pr checks` shows no row with a failing state, or the link of the row has no `runs/<id>` part. |
| `gh not authenticated` | `gh` is not signed in, and the skill cannot get the log. |

## In a ship run

`/sdlc:ship` polls the CI checks in its opt-in `verify-pipeline` step. When
the poll fails under `automation.mode: auto`, ship runs this skill with
`--pr <N>`, `--logs`, `--auto` and `--no-instructions`. So a ship run prints
only the JSON verdict line, and ship runs its own `verify-pipeline` list.

- The `--logs` value is the check list of the poll: the names and the states
  of the checks. It is not the full CI log. The classification of this text
  is less exact than the classification of a failure log. Expect more
  `unknown` results.
- After `fix-applied`, ship commits the fix with `/sdlc:commit`. Then ship
  pauses, because ship does not push the fix commit. You push, and ship polls
  CI again.
- After `abort`, ship skips the step with a warning and continues with its
  `await-remote-review` step.

The steps of a ship run are in
[Pipeline steps](../ship/pipeline-steps.md#verify-pipeline).

## Limits

- **One run, no watch.** The skill runs one time and stops. It does not wait
  for CI to finish. Wait for the failure first, or let `/sdlc:ship` poll.
- **No commit, no push.** The skill never runs `git commit`, `git push`,
  `git tag` or another `git` or `gh` command that changes state, except for a
  follow-up instruction of your config.
- **Inside the project only.** The skill never changes a file outside the
  project root, except for a follow-up instruction of your config.
- **Minimal fixes only.** The skill makes the smallest edit for the failure.
  It does not refactor.

## Follow-up instructions

`/sdlc:verify-pipeline` runs the `verify-pipeline` list of the
`[instructions]` config at the end of a standalone run, for example a post of
the diagnosis to a team channel.
[Follow-up instructions](../../instructions.md) has the config, the merge rule
and the question rules.

| Run | Flow |
|---|---|
| Before | Step 1 → … → Step 4 JSON line |
| After | Step 1 → Step 1b resolve → … → Step 4 JSON line → Step 5 run the list → summary line |

- The list runs only after a `fix-applied` or `proposal` verdict.
- The summary follows the JSON line, and only in a standalone run. Ship passes
  `--no-instructions`, so the ship parser sees only the JSON line.
- The run mode is always interactive. `--auto` does not change it.
- An instruction of the list may run `git` or `gh` commands. This is the one
  exception to the no-commit rule above.
- A failed instruction does not change the verdict. An empty list prints no
  instructions line.

```text
{"status":"fix-applied","filesChanged":["path/a"],"summary":"<one-line summary>"}
Instructions (verify-pipeline): 1 total, 0 done, 1 failed, 0 not run.
- `verify-pipeline #1 (team) failed: <one line of cause>`
```

| Exit route | List runs | What you see |
|---|---|---|
| config error at Step 1b | no | the error and its fix, with no JSON line. Fix the config, then run the skill again. |
| `abort` verdict | no | the `abort` line |
| `--no-instructions` | no | the JSON line only |
| `fix-applied` or `proposal` verdict | yes, after the JSON line | the JSON line, then the summary line |
| you interrupt the list | partial | `Run the remaining instructions by hand. A new run of the skill runs the whole list again unless you pass --no-instructions.` |
