# Commit Skill

Use `/commit` to generate a commit message that matches your project's style, review it, and commit.

What ends up in the commit: every tracked file with working-tree changes (modified or
deleted), except anything under `.sdlc-v2/`, plus whatever you already staged. Untracked
files are never committed. At least one file must be staged before you run the skill.

## When to use it

- You have changes ready to commit, with at least one file staged
- You want the commit message to automatically match your project's existing style (e.g., conventional commits)
- You need to review what goes into the commit before it is made

## Usage

```
/commit [options]
```

### Options

- `--scope <scope>` — Hint for the commit scope (forwarded to the message generator)
- `--type <type>` — Hint for the commit type (forwarded to the message generator)
- `--auto` — Skip interactive approval and commit automatically
- `--force-default-branch` — Deprecated; no effect in this port

`--no-stash` and `--amend` are legacy flags from the old script-driven skill. They are gone
from the argument hint and are ignored if you still type them: this port never stashes and
never amends.

## Workflow

### Step 1: Stage at least one change

Before running `/commit`, stage something — the skill refuses to run with an empty index
("no files staged for commit"):

```bash
git add <files>
```

Staging does **not** narrow the commit. The skill commits every tracked file that has
working-tree changes (except under `.sdlc-v2/`) along with what you staged. Untracked files
are never committed — `git add` a new file first if you want it in.

### Step 2: Run the skill

```
/commit
```

The skill will:

1. **Analyze** your staged diff and recent commits
2. **Generate** a commit message that matches your project's style
3. **Present** the message plus the file breakdown — staged, also-staged tracked changes, paths left out under `.sdlc-v2/`, and untracked files that stay out
4. **Verify** the message against any project-configured commit rules (subject pattern, required trailers, etc.)
5. **Commit** after you approve

The message is drafted from the **staged** diff. If the also-staged tracked files change what
the commit means, choose `edit` and adjust the message before confirming.

### Step 3: Review and confirm

The skill shows:
- The generated commit message (subject and body)
- Files included in the commit and their changes
- Files left out: untracked paths, and tracked paths under `.sdlc-v2/`
- Any detected issues (e.g., breaking rules defined in your project config)

After the commit, the skill reports the result summary from `commit_apply` and names any path
it skipped (`skippedUntrackedPaths`, `skippedTrackedPaths`). Anything named there is still
uncommitted — `git add` it and run `/commit` again to put it in a second commit.

Review the message and choose one of:
- **yes** — commit as shown
- **edit** — make manual changes to the message
- **cancel** — abort without committing

### Step 4: Auto mode

Pass `--auto` to skip the interactive approval prompt:

```
/commit --auto
```

The message is still displayed for visibility, but the commit proceeds automatically. Use this in unattended workflows (e.g., with `/ship`).

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
| Starts | With `/commit`. At least one file must be staged. |
| Ends | After Step 6 shows the commit result. A `cancel`, an error or a rejected commit hook ends the run with no commit. |
| Asks the user | At Step 5: `yes`, `edit` or `cancel`. When the subject does not match `commitConfig.subjectPattern`: `edit subject`, `harden` or `cancel`. `--auto` skips the first question only. |
| Resume | none |

## What the skill detects

### Commit style

The skill learns your project's commit style from recent commits. Common patterns:

- **Conventional commits** — `feat:`, `fix:`, `refactor:`, etc.
- **Scope** — `feat(auth):`, `fix(db):`, etc.
- **Body and trailers** — Multi-line messages with structured details

If your project has a custom `.sdlc-v2/config.toml` with `commit` rules, those are enforced (subject pattern, required body, required trailers).

### WIP commits

If the skill detects work-in-progress commits (subjects starting with `wip:`), it warns you but does not automatically squash them. Squash them manually if you want a single commit.

### Default branch warning

If you're committing directly to the default branch (e.g., `main`), the skill warns you before confirming. This is a warning only — the decision remains yours.

## Quality gates

The generated message is checked against:

- **Style** — Consistent with project patterns
- **Subject length** — ≤ 72 characters
- **Subject pattern** — Matches `commitConfig.subjectPattern` regex (if configured)
- **Accuracy** — Describes only the actual staged diff
- **Imperative mood** — Uses "add" not "adds" or "added"
- **Required body** — Included when type is in `commitConfig.requireBodyFor` (if configured)
- **Required trailers** — All keys from `commitConfig.requiredTrailers` present (if configured)
- **No fabrication** — Everything must trace back to staged changes
- **No WIP prefix** — Subject never starts with `wip:` or `wip(execute):`

## Configuration

The skill respects project settings in `.sdlc-v2/config.toml`:

- `commit.subjectPattern` — Regex the subject must match
- `commit.subjectPatternError` — Custom error message when pattern fails
- `commit.allowedTypes` — Allowed commit types (e.g., `["feat", "fix", "refactor"]`)
- `commit.allowedScopes` — Allowed scopes (optional)
- `commit.requireBodyFor` — Types that require a commit body (e.g., `["feat", "breaking"]`)
- `commit.requiredTrailers` — Trailers that must appear in every commit body (e.g., `["Reviewed-By", "Closes"]`)

To set these up, run:

```
/setup --pr-template
```

Or edit `.sdlc-v2/config.toml` directly.

## Limitations

- **No amend support** — To change the last commit, use `git commit --amend` directly or reset and re-commit.
- **No stash isolation** — Tracked changes and staged files are committed together, so staging a subset does not produce a partial commit. To commit only part of your working tree, move the rest out first with `git stash push -- <paths>`, or commit by hand with plain `git commit`.
- **Untracked files are never committed** — `git add` a new file before running the skill, or it stays out.
- **`.sdlc-v2/` is never committed by the skill** — Changes to the runtime state directory are left in the working tree; commit them yourself if you mean to.
- **No WIP squashing** — WIP commits remain as separate commits; squash them manually if needed.
- **Commit on default branch** — Allowed but warned about; you must confirm.

## Next steps

After committing:

- Run [`/review`](review.md) to review the changes
- Run [`/pr`](pr.md) to create a pull request
- Or use [`/ship`](ship.md) to automate the full workflow

## See also

- [`/review`](review.md) — Review your changes
- [`/pr`](pr.md) — Create a pull request
- [`/ship`](ship.md) — Automated end-to-end pipeline
- [`/setup`](setup.md) — Configure commit rules
