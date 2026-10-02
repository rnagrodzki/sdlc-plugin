# Spec Delta

## MODIFIED Requirements

### Requirement: Input and output fields
The tool SHALL accept one input field and return the output fields below.

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `force` | boolean | optional at call time; missing = `false` | `true` or `false` | Overwrite files that already exist and migrate legacy `.js` files |

| Field | Meaning |
|---|---|
| `root` | Absolute path of the worktree the files were written under |
| `warnings` | Warning strings; an empty list when there are none |
| `files` | One entry per manifest file, in manifest order: `path`, `action`, `installedVersion`, `currentVersion`, `group` |
| `protection` | Branch protection report: `hasRulesets`, `hasClassicProtection`, `defaultBranch`, `rulesetNames`, `notes` |
| `next` | Next-step guidance (see the guidance requirement) |

- Annotations: `Title: "Scaffold CI workflow files"`, `ReadOnly: false`, `Destructive: true`, `Idempotent: true`, `OpenWorld: true`. `OpenWorld` is `true` because the branch protection check runs `gh api`.

#### Scenario: Force omitted
- **WHEN** the caller passes `{}`
- **THEN** the tool runs with `force: false`
- **AND** the advertised input schema does not list `force` as required

#### Scenario: Fresh project
- **WHEN** the tool runs with `force: false` in a project with no `.github/` files
- **THEN** `files` has 8 entries, each with `action: created`
- **AND** `warnings` is an empty list

### Requirement: File manifest
The tool SHALL install exactly the 8 files below, under the active worktree root, in this order.

| `path` | `group` | Legacy path | Version marker in the file |
|---|---|---|---|
| `.github/scripts/check-changelog.cjs` | `changelog` | `.github/scripts/check-changelog.js` | `const CHECK_CHANGELOG_SCRIPT_VERSION = N` |
| `.github/workflows/check-changelog.yml` | `changelog` | none | `# check-changelog-version: N` |
| `.github/scripts/release-on-main.cjs` | `release` | none | `const RELEASE_ON_MAIN_SCRIPT_VERSION = N` |
| `.github/workflows/release-on-main.yml` | `release` | none | `# release-on-main-version: N` |
| `.github/scripts/verify-release-intent.cjs` | `release` | none | `const VERIFY_RELEASE_INTENT_SCRIPT_VERSION = N` |
| `.github/workflows/verify-release-intent.yml` | `release` | none | `# verify-release-intent-version: N` |
| `.github/scripts/promote-release.cjs` | `release` | none | `const PROMOTE_RELEASE_SCRIPT_VERSION = N` |
| `.github/workflows/promote-release.yml` | `release` | none | `# promote-release-version: N` |

- `currentVersion` is the marker number in the embedded file.
- `installedVersion` is the marker number in the file on disk (the new path first, else the legacy path); `null` when neither exists.
- A file with no marker counts as version `1`.
- The tool never creates, reports, or deletes `.github/scripts/retag-release.cjs` or `.github/workflows/retag-release.yml`.

#### Scenario: Files land on disk
- **WHEN** the tool runs in a fresh project
- **THEN** each of the 8 paths exists under the project root

#### Scenario: Old retag-release install left alone
- **WHEN** the project already has `.github/workflows/retag-release.yml`
- **THEN** the file is unchanged after the tool runs with `force: true`
- **AND** no `files` entry names it

### Requirement: Push-auth secret rewrite
The tool SHALL read `version.pushAuth.secretName` from `.sdlc-v2/config.toml`. When it is set and differs from `RELEASE_TOKEN`, the tool SHALL replace every `secrets.RELEASE_TOKEN` with `secrets.<secretName>` in `release-on-main.yml` and `promote-release.yml` before writing them.

- The rewrite does not depend on `version.method`.
- The `secrets.GITHUB_TOKEN` fallback stays in the two files.
- All other files are written unchanged.
- With `secretName` unset or equal to `RELEASE_TOKEN`, all files are byte-identical to the embedded payloads.
- A `[version]` read failure other than "not found" adds warning `reading version config: <cause>` and the run continues without a rewrite.

#### Scenario: Custom secret for any method
- **WHEN** `.sdlc-v2/config.toml` sets `version.method` to `push`, `pr`, or `push-with-secret` and `version.pushAuth.secretName = "MY_BOT"`
- **THEN** the two workflows contain `secrets.MY_BOT` and no `secrets.RELEASE_TOKEN`
- **AND** they still contain `secrets.GITHUB_TOKEN`

#### Scenario: Default secret name leaves payloads unchanged
- **WHEN** `version.pushAuth.secretName` is `RELEASE_TOKEN`
- **THEN** the two workflows are byte-identical to the embedded payloads

### Requirement: Payload content
The tool SHALL install payloads that match this repo's own checked-in `.github/` copies, and whose `.cjs` scripts read only `.sdlc-v2/config.toml`.

- Each embedded payload is byte-identical to the same path under this repo's `.github/`.
- No written `.cjs` file references `.sdlc-v2/config.json`, `.sdlc/config.json`, or `.claude/sdlc.json`.

#### Scenario: Written scripts read the TOML config only
- **WHEN** the tool runs in a fresh project
- **THEN** each of the 4 written `.cjs` files contains `.sdlc-v2/config.toml`
- **AND** none contains `.sdlc/config.json` or `.claude/sdlc.json`

### Requirement: Infrastructure errors
The tool SHALL stop and return an `InfraError` on the file-system failures below. Files written before the failure stay on disk.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| Active worktree root cannot be resolved | `InfraError` | `resolve project root: <cause>` / run `git rev-parse --show-toplevel`, fix, retry |
| Embedded payload missing | `InfraError` | `embedded payload "<name>" not found` / update or reinstall the plugin |
| Installed file cannot be read | `InfraError` | `check installed version of CI script <path>: read <file>: <cause>` / check read permission |
| Legacy file cannot be deleted | `InfraError` | `remove legacy <file>: <cause>` / delete it by hand, rerun with `force: true` |
| Folder cannot be created | `InfraError` | `mkdir <dir>: <cause>` / remove the blocking file, make the parent writable |
| File cannot be written | `InfraError` | `write <file>: <cause>` / check write permission and disk space |

#### Scenario: Unwritable destination
- **WHEN** a destination file cannot be written
- **THEN** the result is an `InfraError` whose message starts with `write `

## ADDED Requirements

### Requirement: Active worktree write root
The tool SHALL resolve its root with `git rev-parse --show-toplevel` (the active worktree), read `.sdlc-v2/config.toml` under that root, write every manifest file under it, and return it as `root`.

#### Scenario: Called from a linked worktree
- **WHEN** the tool runs from a linked worktree of a repo whose main worktree is on another branch
- **THEN** the 8 files are written under the linked worktree
- **AND** no file under the main worktree's `.github/` changes
- **AND** `root` is the linked worktree path
