# Flags and settings

Read this page when you want to change what `/sdlc:review` reviews. It covers the base branch, the scope, the dimensions and the count of parallel reviewers. For the value of the skill and its steps, see the [overview](../review.md).

```text
/sdlc:review --base develop --dry-run
```

This command prints the review plan for a comparison with `develop`. It starts no reviewer.

## Flags

| Flag | What it does | Default |
|---|---|---|
| `--base <branch>` | Compares against this branch instead of the auto-detected base branch. | auto-detected |
| `--dry-run` | Prints the review plan and stops. The skill passes it to `review_prepare` as `dryRun: true`, so the run writes no ledger. | off |

The skill has no other flag. It does not accept `--committed`, `--staged`, `--working`, `--worktree`, `--set-default` or `--dimensions`. The scope and the dimensions are settings, not flags.

## Scope

The scope says which changes the review covers. It is the `scope` key of the `[review]` section in `.sdlc-v2/local.toml`. Change it with `/sdlc:setup`, or edit the file.

```toml
[review]
scope = "working"
```

| Scope | What the review covers | `--base` | Can go to a PR |
|---|---|---|---|
| `all` (default) | The commits of the current branch, compared with the base branch | used | yes |
| `committed` | The same as `all` | used | yes |
| `staged` | The staged changes only | ignored | no |
| `working` | The staged and unstaged changes of tracked files, compared with `HEAD` (`git diff HEAD`) | ignored | no |
| `worktree` | The commits of the branch plus the uncommitted changes of tracked files, compared with the base branch (`git diff <base>`) | used | no |

No scope includes untracked files.

The scopes `staged`, `working` and `worktree` review uncommitted changes. For these scopes the skill does not look for a PR and never offers to post the review to a PR. See [Where the review goes](./results.md#where-the-review-goes).

## Reviewers at the same time

The `maxParallelDimensions` key of the same `[review]` section sets how many reviewers run at the same time.

- The default is 8. The minimum is 1. The value is a whole number.
- Every matching dimension runs, most severe first, in waves of this size.
- A higher value ends a large review sooner and starts more agents at the same time.
- The old key `maxDimensions` is an error.

To change the value, run `/sdlc:setup`, or edit the `[review]` section of `.sdlc-v2/local.toml`.

## Dimensions

A review dimension is one concern, such as security or documentation, with its own review instructions.

- The dimensions of a project are in `.sdlc-v2/review-dimensions/`. Run `/sdlc:setup --dimensions` to add or change them.
- A reviewer reads only the changed files that match its dimension. It reviews only for the concern of its dimension.
- Each finding names a file and a line. One reviewer reports 20 findings at most, the most severe first.
- A dimension that the plan shows as skipped starts no reviewer.

### Large diffs

A reviewer can get a partial diff. This occurs in two cases:

- The count of matched files is above the `max-files` cap (default 100).
- The diff of the dimension is larger than 8000 bytes. The tool then leaves out whole files, the largest first.

The end of the diff file lists the files that the tool left out. The reviewer states in its findings that the diff was partial. It does not call the omitted files clean.

## The dry-run plan

`/sdlc:review --dry-run` prints a review plan with the title `Review Plan (dry run — no agents dispatched)`. The plan has these parts:

| Part | Content |
|---|---|
| Base branch | The branch that the review compares against |
| Changed files | The count of changed files |
| Dimensions | The count of active dimensions, the count of waves, the size of a wave, and the count of skipped dimensions |
| Dimension table | One row for each dimension: `Dimension`, `Files`, `Severity`, `Status` |
| Plan critique | `Uncovered files`, `Over-broad` dimensions and `Suggested dimensions`. Each line shows `none` when it is empty |

The last line tells you to run the command again without `--dry-run`. A dry run deletes its temp files and leaves no ledger.

## Precomputed report data

The preparation step (`review_prepare`) returns `manifestPath` and a `summary` with counts that are already computed:

- `total_dimensions`, `active_dimensions`, `skipped_dimensions`
- `wave_count`
- `total_changed_files`, `uncovered_file_count`
- `suggested_dimensions`

The manifest file at `manifestPath` repeats `summary` and adds these fields:

| Field | Content |
|---|---|
| `scope` | The scope of the review |
| `git` | `commit_count` and `changed_files_count` |
| `pr` | `exists`, and the PR number and URL when a PR exists |
| `plan_critique` | The uncovered files, the over-broad dimensions and `max_parallel_dimensions` |
| `waves` | The dimension names of each wave, most severe first |
| `run_id` | The name of the ledger folder of the run. It is empty for `--dry-run` and when there are zero waves |

Each entry of `dimensions` carries a `worker_id`, which names the ledger entry of that dimension. The skill reads `run_id` and `worker_id` from the manifest. It builds neither value.

These fields describe the input of the review: what is reviewed and with which dimensions. Finding counts and severity counts are not precomputed. Findings do not exist until each reviewer runs, and the skill counts them after the reviewers finish.

## Example dimension: `skill-doc-drift`

This project uses its own review dimensions. The dimension `.sdlc-v2/review-dimensions/skill-doc-drift.md` checks that each `docs/skills/*.md` file agrees with its `SKILL.md` file: the steps, the tool calls, the flags and the mandatory gates. One such gate is the state-first Step 1 of [execute](../execute.md) and [plan](../plan.md).

Copy this pattern when you want each review of your project to catch a difference between the docs and the implementation.

## Tips

- **Dimensions are project-specific.** Each project defines its own files in `.sdlc-v2/review-dimensions/`.
- **The scope is a setting.** You cannot pass `--staged` or `--committed` on the command line. Set `scope` in `.sdlc-v2/local.toml`, or run `/sdlc:setup`.
- **Use a dry run to tune the dimensions.** When a review misses files or takes too long, run `--dry-run`, read the plan and change the dimensions.
