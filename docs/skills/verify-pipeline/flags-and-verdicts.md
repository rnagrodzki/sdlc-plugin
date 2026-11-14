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

| Flag | What it does |
|---|---|
| `--pr <number>` | Names the pull request whose failed CI check the skill analyzes. |
| `--logs <path-or-string>` | Gives the log. A file path makes the skill read the file. Any other value is the log text itself. |
| `--auto` | Lets the skill edit the source files for the three categories that it can fix. Without it, each run ends with a proposal or a stop. |

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

Each run ends with exactly one of these JSON lines on stdout. Logs and
progress go to stderr.

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
`--pr <N>`, `--logs` and `--auto`.

- The `--logs` value is the check list of the poll: the names and the states
  of the checks. It is not the full CI log. The classification of this text
  is less exact than the classification of a failure log. Expect more
  `unknown` results.
- After `fix-applied`, ship commits the fix with `/sdlc:commit`. Then ship
  pauses, because no tool pushes. You push, and ship polls CI again.
- After `abort`, ship skips the step with a warning and continues with its
  `await-remote-review` step.

The steps of a ship run are in
[Pipeline steps](../ship/pipeline-steps.md#verify-pipeline).

## Limits

- **One run, no watch.** The skill runs one time and stops. It does not wait
  for CI to finish. Wait for the failure first, or let `/sdlc:ship` poll.
- **No commit, no push.** The skill never runs `git commit`, `git push`,
  `git tag` or another `git` or `gh` command that changes state.
- **Inside the project only.** The skill never changes a file outside the
  project root.
- **Minimal fixes only.** The skill makes the smallest edit for the failure.
  It does not refactor.
