# tool-commit-prepare Specification

## Purpose
`commit_prepare` gathers read-only commit context (staged, unstaged and untracked files, diffs, recent commits, commit config, branch data) for the `commit` skill. It also writes the whole result to a JSON manifest file so the skill can hand a path to `sdlc:commit-orchestrator`. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Input fields
The tool SHALL accept the input fields below and SHALL NOT read `sessionID`.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `skipConfigCheck` | bool | no | boolean, e.g. `false` | Skip the config-version check. |
| `sessionID` | string | no | plain text, e.g. `""` | Reserved. Not read. |

#### Scenario: sessionID is ignored
- **WHEN** the caller passes any `sessionID` value
- **THEN** the result is the same as with `sessionID: ""`

### Requirement: Worktree roots
The tool SHALL read project config from the main worktree root and SHALL run every git command in the active worktree root.

| Root | Used for |
|---|---|
| Main worktree root | Config-version check, `commit` config section |
| Active worktree root | All git reads |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Main worktree root cannot be resolved | `InfraError` | `resolve main root: <err>` / run from inside a git repository or one of its worktrees |
| Active worktree root cannot be resolved | `InfraError` | `resolve active root: <err>` / change into the repository |

#### Scenario: Called outside a git repository
- **WHEN** the current directory is not inside a git repository
- **THEN** the tool returns an `InfraError` whose message starts with `resolve main root:`
- **AND** the Suggestion says to run `commit_prepare` from inside a git repository or one of its worktrees

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs in a linked worktree
- **THEN** `commitConfig` comes from the main worktree's `.sdlc-v2/` config
- **AND** `staged`, `unstaged` and `untracked` describe the linked worktree

### Requirement: Soft-fail data gathering
The tool SHALL NOT return a tool error for a failed git read, config read or manifest write. It SHALL add a prefixed entry to `warnings` and continue.

| Warning prefix | Trigger |
|---|---|
| `currentBranch:` | Current branch cannot be read |
| `defaultBranch:` | Default branch cannot be read |
| `commitConfig:` | `commit` config section missing or unreadable |
| `staged files:` | `git diff --name-only -z --cached` fails |
| `staged diff:` | `git diff --cached` fails |
| `staged diffStat:` | `git diff --cached --stat` fails |
| `unstaged files:` | `git diff --name-only -z` fails |
| `status:` | `git status --porcelain -z` fails |
| `recentCommits:` | `git log --oneline -15` fails |
| `manifestPath:` | Manifest directory or file cannot be written |

#### Scenario: Manifest write fails
- **WHEN** the manifest temp directory cannot be created
- **THEN** the tool returns without a tool error
- **AND** `manifestPath` is empty
- **AND** `warnings` holds an entry that starts with `manifestPath:`

### Requirement: Config-version check
The tool SHALL run the config-version check unless `skipConfigCheck` is `true`. A failed check SHALL add an entry to `errors`, not a tool error.

- The check fails when `.sdlc-v2/config.json` exists in the main worktree root without `config.toml`.
- The check passes when `.sdlc-v2/config.toml` exists or `.sdlc-v2/` does not exist.
- Error entry text: `config check failed: <reason>`; the reason includes `TOML config required. Run /setup to initialize.`

#### Scenario: Stale config without skip
- **WHEN** `.sdlc-v2/` holds a `config.json` and no `config.toml`
- **AND** `skipConfigCheck` is `false`
- **THEN** `errors` holds an entry containing `config check failed`

#### Scenario: Stale config with skip
- **WHEN** `.sdlc-v2/` holds a `config.json` and no `config.toml`
- **AND** `skipConfigCheck` is `true`
- **THEN** `errors` holds no entry containing `config check failed`

### Requirement: Staged context
The tool SHALL describe the staged changes in `staged` and SHALL add `no files staged for commit` to `errors` when nothing is staged.

| Field | Meaning |
|---|---|
| `staged.files` | Paths from `git diff --name-only -z --cached`, as raw names (never C-quoted) |
| `staged.fileCount` | Length of `staged.files` |
| `staged.diff` | Output of `git diff --cached`, truncated when over budget |
| `staged.diffStat` | Output of `git diff --cached --stat` |
| `staged.diffTruncated` | `true` when the full staged diff is over 8000 bytes |
| `staged.truncatedFiles` | Files whose diff was left out of `staged.diff` |

#### Scenario: Nothing staged
- **WHEN** the index has no staged changes
- **THEN** `errors` holds `no files staged for commit`
- **AND** `next` is `Fix the errors above, then call commit_prepare again.`

#### Scenario: One file staged
- **WHEN** `hello.txt` is staged
- **THEN** `staged.files` is `["hello.txt"]` and `staged.fileCount` is `1`

### Requirement: Staged diff truncation
The tool SHALL cap `staged.diff` at an 8000-byte budget by keeping whole per-file diffs, and SHALL list every left-out file in `staged.truncatedFiles`.

- Per-file diffs are kept largest first; at least one file diff is always kept.
- Smaller file diffs that still fit are kept after a larger one is skipped.
- A footer in `staged.diff` lists the omitted files.
- `staged.diffStat` is never truncated.

#### Scenario: Diff within budget
- **WHEN** the full staged diff is 8000 bytes or less
- **THEN** `staged.diff` is the full diff
- **AND** `staged.diffTruncated` is `false` and `staged.truncatedFiles` is empty

#### Scenario: Diff over budget
- **WHEN** the full staged diff is over 8000 bytes
- **THEN** `staged.diffTruncated` is `true`
- **AND** `staged.truncatedFiles` names each file whose diff is not in `staged.diff`

### Requirement: Unstaged and untracked files
The tool SHALL list in `unstaged` every tracked path whose working tree differs from the index, and SHALL list untracked entries in `untracked`. A staged-only change SHALL NOT appear in `unstaged`.

| Field | Meaning |
|---|---|
| `unstaged.files` | Paths from `git diff --name-only -z` (working tree vs index), as raw names (never C-quoted); the same comparison `commit_apply` uses to pick extra tracked paths |
| `unstaged.fileCount` | Length of `unstaged.files` |
| `unstaged.hasChanges` | `true` when `unstaged.fileCount` > 0 |
| `untracked.files` | `??` entries from `git status --porcelain -z`, as raw names (never C-quoted); a wholly untracked directory is one entry with a trailing `/` |
| `untracked.fileCount` | Length of `untracked.files` |

#### Scenario: Staged-only file not listed as unstaged
- **WHEN** `a.ts` is staged and has no later working-tree edit
- **THEN** `staged.files` holds `a.ts`
- **AND** `unstaged.files` does not hold `a.ts`

#### Scenario: Staged file with a later working-tree edit
- **WHEN** `a.ts` is staged and then edited again in the working tree
- **THEN** `staged.files` holds `a.ts`
- **AND** `unstaged.files` holds `a.ts`

#### Scenario: Everything staged
- **WHEN** every change in the working tree is staged
- **THEN** `unstaged.files` is empty
- **AND** `unstaged.hasChanges` is `false`

#### Scenario: Untracked directory
- **WHEN** the directory `.sdlc-v2/runs/` holds only untracked files
- **THEN** `untracked.files` holds one entry for the directory, ending in `/`

#### Scenario: Untracked non-ASCII name
- **WHEN** the untracked file `été notes.txt` exists
- **THEN** `untracked.files` is `["été notes.txt"]`

#### Scenario: Staged and unstaged non-ASCII names
- **WHEN** the new file `été staged.txt` is staged and the tracked file `naïve notes.txt` is edited but not staged
- **THEN** `staged.files` is `["été staged.txt"]`
- **AND** `unstaged.files` is `["naïve notes.txt"]`

### Requirement: Branch information
The tool SHALL report `currentBranch`, `defaultBranch` and `onDefaultBranch`. `onDefaultBranch` SHALL be `true` only when `currentBranch` is non-empty and equals `defaultBranch`.

#### Scenario: On the default branch
- **WHEN** the active branch is `main` and the default branch is `main`
- **THEN** `onDefaultBranch` is `true`

#### Scenario: Current branch unknown
- **WHEN** the current branch cannot be read
- **THEN** `currentBranch` is empty and `onDefaultBranch` is `false`
- **AND** `warnings` holds an entry that starts with `currentBranch:`

### Requirement: Commit config
The tool SHALL return the project config's `commit` section as `commitConfig`, and `null` when that section cannot be read.

- The section carries the keys the orchestrator reads: `subjectPattern`, `subjectPatternError`, `allowedTypes`, `allowedScopes`, `requireBodyFor`, `requiredTrailers`.
- The tool does not validate these keys.

#### Scenario: No commit section
- **WHEN** the project config has no `commit` section
- **THEN** `commitConfig` is `null`
- **AND** `warnings` holds an entry that starts with `commitConfig:`

### Requirement: Recent commits and last commit subject
The tool SHALL return up to 15 recent commits in `recentCommits` and the subject of the `HEAD` commit in `lastCommitMessage`.

| Field | Source | When unavailable |
|---|---|---|
| `recentCommits` | `git log --oneline -15`, one entry per line | Empty array, plus a `recentCommits:` warning on git failure |
| `lastCommitMessage` | `git log -1 --format=%s`, trimmed | `null` |

#### Scenario: Repository with one commit
- **WHEN** the repository has exactly one commit
- **THEN** `recentCommits` holds one entry
- **AND** `lastCommitMessage` is that commit's subject

### Requirement: WIP commit detection
The tool SHALL list in `wipSquash.commits` each commit between the branch fork point and `HEAD` whose subject starts with `wip(` or `wip:`, ignoring case. It SHALL NOT squash or rewrite any commit.

- Fork point: `git merge-base <upstream> HEAD`, where `<upstream>` is `@{upstream}`, or the default branch when there is no upstream.
- Each entry is `<full sha>\t<subject>`.
- `wipSquash.stagedClean` is `true` when nothing is staged. It stays `false` when the fork-point lookup fails.
- Any failure in this lookup yields an empty `wipSquash.commits` and no warning.

#### Scenario: WIP commit on a feature branch
- **WHEN** the branch `feat/test-wip` has a commit with subject `wip(execute): work in progress` after its fork point
- **THEN** `wipSquash.commits` is non-empty

#### Scenario: No upstream and no default branch
- **WHEN** neither `@{upstream}` nor the default branch can be resolved
- **THEN** `wipSquash.commits` is empty

### Requirement: Fixed legacy fields
The tool SHALL always return the legacy fields `flags`, `migration` and `branchGuard` with the fixed values below.

| Field | Value |
|---|---|
| `flags.skipConfigCheck` | Echo of input `skipConfigCheck` |
| `flags.noStash`, `flags.amend`, `flags.auto`, `flags.noSquashWip`, `flags.forceDefaultBranch` | Always `false` |
| `flags.scope`, `flags.type` | Always `null` |
| `migration` | Always `null` |
| `branchGuard` | Always `{ ok: true }` |

#### Scenario: Any call
- **WHEN** `commit_prepare` is called with `skipConfigCheck: true`
- **THEN** `flags.skipConfigCheck` is `true` and `flags.auto` is `false`
- **AND** `branchGuard.ok` is `true` and `migration` is `null`

### Requirement: Output shape and next step
The tool SHALL always return every top-level output field, with list fields as arrays (never `null`), and SHALL set `next` from the `errors` list.

| Field | Meaning |
|---|---|
| `errors` | Blocking problems; the caller must stop when non-empty |
| `warnings` | Non-blocking problems (see Soft-fail data gathering) |
| `currentBranch`, `defaultBranch`, `onDefaultBranch` | Branch information |
| `flags`, `migration`, `branchGuard` | Fixed legacy fields |
| `commitConfig` | `commit` config section or `null` |
| `staged`, `unstaged`, `untracked` | File context |
| `recentCommits`, `lastCommitMessage` | Style context |
| `wipSquash` | WIP commit detection |
| `next` | Next step text |
| `manifestPath` | Path to the JSON manifest, or empty |

| `errors` | `next` |
|---|---|
| Empty | `Call commit_apply with the prepared payload.` |
| Non-empty | `Fix the errors above, then call commit_prepare again.` |

#### Scenario: Staged change and valid state
- **WHEN** one file is staged and `skipConfigCheck` is `true`
- **THEN** the output holds all top-level fields listed above
- **AND** `next` is `Call commit_apply with the prepared payload.`

### Requirement: Manifest file
The tool SHALL write the whole result as JSON to `manifest.json` in a new `sdlc-commit-manifest-*` directory under the system temp directory, and SHALL return that path in `manifestPath`.

- The manifest's own `manifestPath` field equals the returned path.
- On success the file and directory stay on disk; the tool never removes the directory it just wrote.
- After a successful write, the tool removes every other `sdlc-commit-manifest-*` directory in the same temp root whose modification time is more than 24 hours old. Younger ones and other directories are kept. Errors during this cleanup are ignored and add no warning.
- On a directory-create or file-write failure: `manifestPath` is empty and a `manifestPath:` warning is added. After a file-write failure the new directory is removed.
- The file-write warning text contains `write manifest file`.

#### Scenario: Manifest written
- **WHEN** the temp directory and file writes succeed
- **THEN** `manifestPath` names a readable file
- **AND** decoding that file gives the same `currentBranch` and `manifestPath` as the result

#### Scenario: Manifest file write fails
- **WHEN** the temp directory is created but the file write fails
- **THEN** `manifestPath` is empty
- **AND** `warnings` holds an entry containing `manifestPath` and `write manifest file`
- **AND** no `sdlc-commit-manifest-*` directory is left in the temp root

#### Scenario: Stale manifest directories removed
- **WHEN** the temp root holds an `sdlc-commit-manifest-*` directory 48 hours old, one 1 hour old, and an unrelated directory 48 hours old
- **AND** `commit_prepare` writes a new manifest
- **THEN** the 48-hour-old manifest directory is removed
- **AND** the 1-hour-old manifest directory, the unrelated directory and the new manifest directory remain

### Requirement: Tool annotations
The tool SHALL declare the annotations below.

| Annotation | Value |
|---|---|
| Title | `Prepare commit context` |
| ReadOnly | `true` |
| Idempotent | `true` |
| OpenWorld | `false` |

#### Scenario: Listing tools
- **WHEN** an MCP client lists tools
- **THEN** `commit_prepare` is marked read-only, idempotent and not open-world
