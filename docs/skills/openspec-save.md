# /sdlc:openspec-save

Get your OpenSpec change reviewed and merged as its own pull request, before any implementation code exists.

## Why use it

`/sdlc:plan` with the choice **Create OpenSpec change** stages the spec of your change in a folder outside the tracked tree.
No branch and no pull request holds the spec yet.
This skill moves the change onto its own branch, commits it and opens a pull request that holds only the spec.
Your reviewers merge the spec first. Then you ship the implementation.

## Example

The plan `add-widget.md` staged the change `add-widget`. On the default branch, with no uncommitted change in tracked files, run:

    /openspec-save /Users/me/.claude/plans/add-widget.md

1. The skill creates the branch `openspec/add-widget` and moves the change into `openspec/changes/add-widget/`.
2. The commit skill drafts the commit message and asks you to approve it.
3. The pr skill asks for release intent. Choose **Skip release (acknowledged)**: the PR holds only the OpenSpec change.
4. The skill prints the change name, the branch and the PR URL.

Merge that PR. Then create a new branch from the default branch and run `/sdlc:ship --plan <plan-path>`.

`/openspec-save` is the short name. The full command name is `/sdlc:openspec-save`.

## What you get

- **Spec review before any code exists.** The change gets its own branch `openspec/<change>`, its own commit and its own PR.
- **One command for the hand work.** The skill creates the branch, moves and stages the change, then runs the commit skill and the pr skill.
- **Your decisions stay yours.** The commit skill and the pr skill ask you directly, and the skill never answers a question for you.
- **A stopped run is safe to repeat.** Run the skill again on the saved branch, and the run continues at the commit or at the PR.
- **No half-saved change.** The tool checks that each file of the change is in the git index before it marks the plan as saved.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 1: Save | tool | Calls `openspec_save` with the absolute plan path. The tool creates the branch `openspec/<change>`, moves the change into `openspec/changes/<change>/` and stages it. | The list of staged files | no | always |
| Step 2: Commit | skill | Runs the commit skill with `--type docs --scope openspec`, and with `--auto` when you passed it. | The commit on the branch | yes | `stagedFiles` has paths |
| Step 3: PR | skill | Runs the pr skill with no `--auto`. It asks you for release intent and approval. Choose **Skip release (acknowledged)**. | The PR URL | no | always |
| Step 4: Report | skill | Prints the change name, the branch and the PR URL. It tells you to merge the PR and then run `/sdlc:ship --plan <plan-path>`. | The report | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:openspec-save <plan-path>` or `--plan <path>`. The plan path is required. |
| Ends | After the Step 4 report. It also ends at once when the tool fails or you decline the commit or the PR. It reports the branch. |
| Asks the user | In the commit skill and the pr skill, which ask you directly. It never answers for you. `--auto` skips only the commit approval. |
| Resume | Run the skill again on the saved branch. The saved plan header makes `openspec_save` return `already`, and the run continues at Step 2 or Step 3. |

## More examples

**Name the plan with a flag, and skip the commit approval:**

    /openspec-save --plan /Users/me/.claude/plans/add-widget.md --auto

The commit skill does not ask you to approve its message. The pr skill still asks for release intent and for approval of the PR text.

| Flag | What it does |
|---|---|
| `--plan <path>` | Names the plan file. Same as the positional `<plan-path>`. |
| `--auto` | Skips the approval question of the commit skill. The pr skill still asks you. |

**Continue a stopped run:**

    /openspec-save /Users/me/.claude/plans/add-widget.md

You declined the commit in the first run, so the branch `openspec/add-widget` holds the staged change.
Run the same command again on that branch. The tool returns `already`, and the run continues at the commit.
When the commit exists and the PR does not, the run continues at the PR.

### The merge-first flow

1. `/plan` stages the change and writes the `**OpenSpec-Staging:**` header line in the plan file.
2. `/openspec-save` creates the branch `openspec/<change>`, moves the change into `openspec/changes/<change>/`, commits it, and opens a PR.
3. You and your reviewers merge that PR into the default branch.
4. You create a new branch from the default branch and run `/ship` with the same plan file.

The OpenSpec PR and the code PR are two separate PRs.

`/plan` stages the change under `.sdlc-v2/openspec-staging/<change>/` of the main worktree. When the skill can find the main worktree, it runs from any worktree of the repo.
A change that an older plugin staged sits in that folder of the active worktree, and the skill can fall back to it.
When the skill cannot find the main worktree, it uses only the folder of the active worktree and prints the warning `Main worktree not found (...)`.
[Where the skill finds the staged change](./openspec-save/flags-and-prerequisites.md#where-the-skill-finds-the-staged-change) has the exact rules.

## Reference

- [Flags and prerequisites](./openspec-save/flags-and-prerequisites.md): the two flags, the rules for the plan path, the state the repository needs, and where the skill finds the staged change.
- [Stops and repeat runs](./openspec-save/stops-and-repeat-runs.md): what a save changes, what stops a run, and how the next run continues.

## Related skills

- [/sdlc:plan](./plan.md): stages the OpenSpec change and writes the header line that this skill reads.
- [/sdlc:commit](./commit.md): commits the staged change. It runs inside this skill.
- [/sdlc:pr](./pr.md): opens the PR, or updates the open PR of the branch. It runs inside this skill.
- [/sdlc:ship](./ship.md): run it on a new branch from the default branch after the OpenSpec PR is merged.
