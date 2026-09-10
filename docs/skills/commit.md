# Commit Skill

Use `/commit` to generate a commit message that matches your project's style, review it, and commit staged changes.

## When to use it

- You have staged changes ready to commit
- You want the commit message to automatically match your project's existing style (e.g., conventional commits)
- You need to verify the staged changes before committing

## Usage

```
/commit [options]
```

### Options

- `--no-stash` — Deprecated; no effect in this port
- `--scope <scope>` — Hint for the commit scope (forwarded to the message generator)
- `--type <type>` — Hint for the commit type (forwarded to the message generator)
- `--amend` — Not supported; use other tools to amend
- `--auto` — Skip interactive approval and commit automatically
- `--force-default-branch` — Deprecated; no effect in this port

## Workflow

### Step 1: Stage your changes

Before running `/commit`, stage the exact files you want to commit:

```bash
git add <files>
```

The skill works with staged changes only. Unstaged changes are preserved but not included in the commit.

### Step 2: Run the skill

```
/commit
```

The skill will:

1. **Analyze** your staged diff and recent commits
2. **Generate** a commit message that matches your project's style
3. **Present** the message and staged file summary for your review
4. **Verify** the message against any project-configured commit rules (subject pattern, required trailers, etc.)
5. **Commit** after you approve

### Step 3: Review and confirm

The skill shows:
- The generated commit message (subject and body)
- Files included in the commit and their changes
- Any detected issues (e.g., breaking rules defined in your project config)

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

## What the skill detects

### Commit style

The skill learns your project's commit style from recent commits. Common patterns:

- **Conventional commits** — `feat:`, `fix:`, `refactor:`, etc.
- **Scope** — `feat(auth):`, `fix(db):`, etc.
- **Body and trailers** — Multi-line messages with structured details

If your project has a custom `.sdlc-v2/config.json` with `commit` rules, those are enforced (subject pattern, required body, required trailers).

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

The skill respects project settings in `.sdlc-v2/config.json`:

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

Or edit `.sdlc-v2/config.json` directly.

## Limitations

- **No amend support** — To change the last commit, use `git commit --amend` directly or reset and re-commit.
- **No stash isolation** — All staged changes are committed together. To commit only part of your working tree, stage exactly what you want first.
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
