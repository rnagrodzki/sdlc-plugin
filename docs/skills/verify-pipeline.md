# /sdlc:verify-pipeline

Find why a CI check failed on your pull request and get a proposed fix, or a minimal applied fix, in one command.

## Why use it

A CI check fails on your pull request. You open the run, search the log and decide what kind of failure it is.
`/sdlc:verify-pipeline` reads the failure log, puts the failure in a category and gives you a diagnosis with a suggested patch.
With `--auto`, it edits the source files for a lint failure, a test failure or a type error.
You commit and push the fix. The skill never does.

## Example

A CI check failed on pull request 42. Ask for the cause:

```text
/sdlc:verify-pipeline --pr 42
```

The skill finds the first failed check with `gh pr checks`, reads its failure log and puts the failure in 1 of 7 categories. Then it prints one JSON line, the verdict. Without `--auto`, the verdict is always a proposal:

- `"status":"proposal"`: the skill changed no file.
- `"summary"`: the diagnosis.
- `"suggestedPatch"`: the fix, as a diff or as text.

## What you get

- **No search for the log.** With `--pr` alone, the skill finds the first failed check and gets its failure log with `gh`.
- **The kind of failure, named.** Each log gets 1 of 7 categories: `lint`, `test-failure`, `type-error`, `build-error`, `dependency`, `infra` or `unknown`.
- **A small fix, not a refactor.** With `--auto`, the skill makes the minimal edit for a `lint`, `test-failure` or `type-error` failure.
- **Control of your git history.** The skill never runs `git commit`, `git push` or `git tag`, and it changes no file outside the project root.
- **CI fixes inside a ship run.** `/sdlc:ship` runs the skill when its CI poll fails under `automation.mode: auto`, then commits a fix that the skill applied.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 1: CONSUME — parse args, load logs (R1, R6) | tool | Reads `--pr`, `--logs` and `--auto`. With `--pr` only, it gets the failing check log with `gh`. It stops with an `abort` verdict when no log is found. | The log text | no | always |
| Step 2: CLASSIFY — invoke the classification tool (R2) | tool | Calls `verify_pipeline_classify` with the log text. | The category and the matched signals | no | always |
| Step 3: PROPOSE OR APPLY (R3, R4, R9) | skill | For `lint`, `test-failure` and `type-error` with `--auto`, it edits the source files. For every other case it writes a proposal. It runs no `git commit` or `git push`. | The fix or the proposal | no | always |
| Step 4: VERDICT — single JSON line on stdout (R5) | skill | Prints one JSON line: `fix-applied`, `proposal` or `abort`. | The verdict line for the caller | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:verify-pipeline`. `--pr <number>` or `--logs <path-or-string>` is required. `/sdlc:ship` runs it when a CI poll fails under `automation.mode: auto`. |
| Ends | After Step 4 prints the verdict line. |
| Asks the user | Nothing. The skill has no prompt. |
| Resume | none |

## More examples

**Let the skill apply the fix:**

```text
/sdlc:verify-pipeline --pr 42 --auto
```

For a `lint`, `test-failure` or `type-error` failure, the skill edits the source files. The verdict is `fix-applied`, with the changed files in `filesChanged`. The edit stays in your working tree with no commit. Each other category gets a proposal.

**Classify a log that you already have:**

```text
/sdlc:verify-pipeline --logs build-output.log
```

The skill reads the log from the file. A value that is not a file path is the log text itself.

## Reference

- [Flags and verdicts](./verify-pipeline/flags-and-verdicts.md): the 3 flags, the source of the log, the 7 categories, the 3 verdict lines, the stop reasons, the ship run and the limits.

## Related skills

- [/sdlc:pr](./pr.md): opens the pull request whose CI checks this skill analyzes.
- [/sdlc:ship](./ship.md): polls CI in its opt-in `verify-pipeline` step and runs this skill when the poll fails.
- [/sdlc:commit](./commit.md): commits the fix after a `fix-applied` verdict. In a ship run, ship runs it for you.
