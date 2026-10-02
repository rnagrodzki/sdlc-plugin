# base-branch Specification

## Purpose
Gives every SDLC tool one integration branch to compare against and integrate from, so projects that integrate on `develop` work the same as projects that integrate on `main`.

## Requirements

### Requirement: Base branch configuration
The system SHALL read the integration branch from `[git] baseBranch` in `.sdlc-v2/config.toml`, and SHALL fall back to the repository default branch (`refs/remotes/origin/HEAD`, then local `main`, then local `master`) when the key is absent or empty.

```diff
+[git]
+baseBranch = "develop"   # optional; empty or absent = repository default branch
```

- `plugins/sdlc/templates/config.toml` and `plugins/sdlc/schemas/sdlc-config.schema.json` both declare `[git]` with `baseBranch` (string).

#### Scenario: Configured
- **WHEN** `.sdlc-v2/config.toml` has `[git] baseBranch = "develop"`
- **THEN** the resolved base branch is `develop`

#### Scenario: Not configured
- **WHEN** `[git]` is absent and `origin/HEAD` points to `main`
- **THEN** the resolved base branch is `main`

#### Scenario: Configured branch missing on the remote
- **WHEN** `baseBranch = "develop"` and `git fetch origin develop` fails
- **THEN** the calling step reports `base branch "develop" not found on origin` and does not fall back silently


### Requirement: Every base-relative operation uses the resolved base branch
Each operation below SHALL use the resolved base branch instead of the repository default branch.

| Operation | Before | After |
|---|---|---|
| execute pre-execution rebase (`--rebase`) | `origin/<default>` | `origin/<base>` |
| execute wave base sync | — | `origin/<base>` |
| ship `rebase` step | `origin/<default>` | `origin/<base>` |
| pr: new PR base when `--base` is not passed | repository default | `<base>` |
| pr / commit: commit list range | `<default>..HEAD` | `<base>..HEAD` |
| review: diff base when `--base` is not passed | repository default | `<base>` |

- An explicit `--base <branch>` on pr or review still wins over config.
- The "on the default branch" checks (execute workspace derivation, ship default-branch push gate) also treat `<base>` as protected.

#### Scenario: PR targets develop
- **WHEN** `baseBranch = "develop"` and `/sdlc:pr` creates a new PR without `--base`
- **THEN** `gh pr create` receives `--base develop`

#### Scenario: Explicit flag wins
- **WHEN** `baseBranch = "develop"` and `/sdlc:review --base main` runs
- **THEN** the review diff is computed against `main`

#### Scenario: Ship rebase
- **WHEN** `baseBranch = "develop"` and ship reaches its `rebase` step
- **THEN** the skill runs `git fetch origin develop` and checks `git merge-base --is-ancestor origin/develop HEAD`

