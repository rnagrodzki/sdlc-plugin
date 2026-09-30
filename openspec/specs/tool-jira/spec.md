# tool-jira Specification

## Purpose
The `jira` MCP tool manages the local Jira project cache, the per-project description templates, link checks with markdown-to-ADF conversion, and the critique/approval artifacts of the `jira` skill. It never calls the Jira API; the `jira` skill is its caller. Output follows docs/mcp-output-contract.md.

## Requirements

### Requirement: Action dispatch
The tool SHALL select its operation from the `action` input and SHALL reject any value outside the action enum with a `DomainError`.

| Action | Required fields | Optional fields | Files written |
|---|---|---|---|
| `check` | `key` | `cacheDir`, `site`, `templatesDir`, `skipConfigCheck` | `cacheDir` (created) |
| `check-default-project` | — | `skipConfigCheck` | — |
| `load` | `key` | `cacheDir`, `site`, `skipConfigCheck` | `cacheDir` (created) |
| `save` | `key`, `data` | `cacheDir`, `site`, `skipConfigCheck` | cache file |
| `save-field` | `key`, `fieldName`, `data` | `cacheDir`, `site`, `skipConfigCheck` | cache file |
| `templates` | `key` | `cacheDir`, `site`, `templatesDir`, `skipConfigCheck` | `cacheDir` (created) |
| `init-templates` | `key` | `cacheDir`, `site`, `templatesDir`, `skipConfigCheck` | `.sdlc-v2/jira-templates/<Type>.md` |
| `clear` | `key` | `cacheDir`, `site`, `skipConfigCheck` | deletes cache file(s) |
| `copy-template` | `templateType`, `templateFrom` | `templatesDir`, `skipConfigCheck` | `.sdlc-v2/jira-templates/<templateType>.md` |
| `validate-body` | — | `markdownBody`, `cacheDir`, `skipConfigCheck` | — |
| `write-critique` | `hash`, `data` | `skipConfigCheck` | `.sdlc-v2/state/artifacts/critique-<hash>.json` |
| `write-approval` | `hash` | `skipConfigCheck` | `.sdlc-v2/state/artifacts/approval-<hash>.token` |

| Field | Type | Required | Encoding | Meaning |
|---|---|---|---|---|
| `action` | string (enum) | yes | plain text | One of the 12 actions above |
| `key` | string | per action | plain text, e.g. `PROJ` | Jira project key; trimmed and uppercased; names the cache file `<KEY>.json` |
| `cacheDir` | string | no | path | Flat cache dir `<cacheDir>/<KEY>.json` instead of the home layout; for `validate-body`, the Jira site discovery root |
| `site` | string | no | sanitized host, e.g. `acme_atlassian_net` | Picks one site subdirectory of the home cache; ignored by `validate-body` |
| `templatesDir` | string | no | path | Overrides discovery of the shipped default templates directory |
| `data` | object | per action | JSON object | Payload for `save`, `save-field`, `write-critique` |
| `fieldName` | string | `save-field` | plain text, e.g. `userMappings` | Top-level cache key to merge or overwrite |
| `templateType` | string | `copy-template` | plain text, e.g. `Sub-bug` | Destination issue-type name |
| `templateFrom` | string | `copy-template` | plain text, e.g. `Task` | Source default template name without `.md` |
| `markdownBody` | string | no | markdown | Body to link-check and convert to ADF |
| `hash` | string | `write-critique`, `write-approval` | alphanumeric, e.g. `a1b2c3d4e5f6` | Names the artifact file |
| `skipConfigCheck` | bool | no | JSON bool | Skips the config-version gate |

#### Scenario: Unknown action
- **WHEN** `action` is `bogus`
- **THEN** the tool returns a `DomainError` whose message starts with `unknown jira action "bogus"` and names the running sdlc version and commit
- **AND** the suggestion says to pass a listed action or update the sdlc plugin

### Requirement: Key required for cache and template actions
The tool SHALL return a `DomainError` `key is required` when `key` is empty or whitespace for every action except `validate-body`, `check-default-project`, `copy-template`, `write-critique`, and `write-approval`.

- `key` is a Jira project key (e.g. `PROJ`), not an issue key.
- The exempt actions never read `key`.
- The key is trimmed and uppercased before use (`foo` becomes `FOO`).

#### Scenario: Missing key on check
- **WHEN** `action` is `check` and `key` is empty
- **THEN** the tool returns `DomainError` `key is required`

#### Scenario: Key not needed for copy-template
- **WHEN** `action` is `copy-template` with `templateType` and `templateFrom` set, `key` empty, and the source template present
- **THEN** the tool copies the template and returns `copied: true`

#### Scenario: Key not needed for validate-body
- **WHEN** `action` is `validate-body` and `key` is empty
- **THEN** the tool runs the action without error

#### Scenario: Key uppercased
- **WHEN** `action` is `check` with `key` `foo`
- **THEN** the result has `projectKey` `FOO`

### Requirement: Config-version gate
Unless `skipConfigCheck` is `true`, the tool SHALL check the project config version before any action and, when the config is stale, SHALL return a non-error result that holds only an `errors` list and SHALL NOT run the action.

- Stale means a JSON-era `.sdlc-v2/config.json` exists under the main root but `.sdlc-v2/config.toml` does not.
- A `.sdlc-v2/` directory that holds only tool data (no `config.toml`, no `config.json`) is not stale.
- The `errors` entry starts with `config-version: ` and contains `TOML config required. Run /setup to initialize.`
- The gate runs before the key check.

#### Scenario: Stale config
- **WHEN** `.sdlc-v2/config.json` exists without `.sdlc-v2/config.toml` and `skipConfigCheck` is `false`
- **THEN** the result has only an `errors` list with one `config-version: ...` entry
- **AND** no action runs and no file is written

#### Scenario: Gate skipped
- **WHEN** the same project is called with `skipConfigCheck` `true`
- **THEN** the action runs and returns its normal result

### Requirement: Data writes do not make the config stale
The tool's own writes under `.sdlc-v2/` (`copy-template`, `init-templates`, `write-critique`, `write-approval`) SHALL NOT make a later config-version gate, in this tool or any other tool, report the project as stale.

#### Scenario: Writes in a project without config
- **WHEN** a project has no `.sdlc-v2/config.toml` and no `.sdlc-v2/config.json`
- **AND** `write-critique`, `write-approval`, and `init-templates` run and create `.sdlc-v2/`
- **THEN** a later `check` call with `skipConfigCheck` `false` returns its normal result with no `config-version:` error
- **AND** no `config.toml` is written

### Requirement: Project-relative paths use the main worktree root
The tool SHALL resolve every project-relative path (`.sdlc-v2/config.toml`, `.sdlc-v2/jira-templates/`, `.sdlc-v2/state/artifacts/`) against the main worktree root, also when called from a linked worktree.

#### Scenario: Not in a git repository
- **WHEN** the main root cannot be resolved
- **THEN** the tool returns `InfraError` `resolve main root: <cause>`
- **AND** the suggestion says to run jira from inside a git repository or one of its worktrees

### Requirement: Cache file location
The tool SHALL locate the project cache file for `check`, `load`, `save-field`, `templates`, and `init-templates` by the rules in this table.

| Inputs | Cache path | Result |
|---|---|---|
| `cacheDir` set | `<cacheDir>/<KEY>.json` | `cacheDir` is created if missing |
| no `cacheDir`, `site` set, home root exists | `~/.sdlc-cache/jira/<site>/<KEY>.json` | path used even if the file is missing |
| no `cacheDir`, `site` set, home root missing | none | warning `Home cache root <root> does not exist. Run cache initialization first (omit site or use force-refresh).` |
| no `cacheDir`, no `site`, one site dir has `<KEY>.json` | that file | — |
| no `cacheDir`, no `site`, 2+ site dirs have `<KEY>.json` | none | ambiguous: `candidateSites` lists the site dirs |
| no `cacheDir`, no `site`, no match | none | cache miss |

- When `cacheDir` resolves inside the current working directory, the tool writes `<cacheDir>/.gitignore` with `*` if that file does not exist.
- Legacy cache locations (`.sdlc-v2/jira-cache/`, `.claude/jira-cache/`) are not read or migrated.

#### Scenario: Explicit cacheDir inside the working tree
- **WHEN** `cacheDir` is a new directory under the current working directory
- **THEN** the tool creates it and writes `.gitignore` with content `*`

#### Scenario: Unusable cacheDir
- **WHEN** `cacheDir` cannot be created (for example it sits below a regular file)
- **THEN** `check`, `load`, `save-field`, `templates`, and `init-templates` return `InfraError` `resolve cache path: <cause>`
- **AND** `save` and `clear` return `InfraError` `resolve cache path under cacheDir "<cacheDir>": <cause>`, whose suggestion names `cacheDir`

### Requirement: check reports cache state
The `check` action SHALL report whether the cache exists, whether it is fresh, which sections are missing, and the template coverage, and SHALL report cache problems as result fields, not as errors.

| Field | Meaning |
|---|---|
| `exists` | `true` when the cache file exists |
| `fresh` | freshness per the rules below |
| `ageHours` | hours since `lastUpdated`, rounded to 2 decimals; `null` when unknown |
| `maxAgeHours` | cache `maxAgeHours`; `0` when absent |
| `projectKey` | uppercased key |
| `cachePath` | resolved path, or `null` |
| `candidateSites` | site dirs when the key is cached under 2+ sites; else empty |
| `sections` | per-section `present` flag plus counts (see below) |
| `templates` | `customCount`, `defaultCount`, `customTypes`, `uncoveredTypes`, `fallbacks` |
| `missing` | sections not present, in order `cloudId`, `currentUser`, `project`, `issueTypes`, `fieldSchemas`, `workflows`, `linkTypes`, `userMappings`; `["all"]` when no usable cache |
| `flags` | `skipWorkflowDiscovery` (always `false`), `site` (input or `null`) |
| `errors` | soft errors (config read, invalid JSON) |
| `warnings` | resolution and completeness warnings |

- `ageHours`, `maxAgeHours`, `sections`, and `templates` appear only when the cache file exists and parses.
- `sections` counts: `issueTypes.count`, `fieldSchemas.issueTypesWithSchemas`, `workflows.issueTypesWithWorkflows`, `workflows.incomplete`, `linkTypes.count`, `userMappings.count`.
- `fresh` is `true` when `lastUpdated` parses as RFC 3339 and either `maxAgeHours` is `0` (permanent cache) or the age is below `maxAgeHours`.
- Warning `Cache file has no lastUpdated field; freshness cannot be determined.` when `lastUpdated` is missing or not RFC 3339.
- Warning `Workflow data missing for issue types: <list>` when a cached issue type has no `workflows` entry.
- Issue-type lists are sorted alphabetically.

#### Scenario: No cache
- **WHEN** `check` runs with a `cacheDir` that has no `FOO.json`
- **THEN** `exists` is `false` and `missing` is `["all"]`

#### Scenario: Ambiguous home cache
- **WHEN** `check` runs without `site` and `FOO.json` exists under two site dirs
- **THEN** `exists` is `false` and `candidateSites` lists both site dirs
- **AND** `warnings` has `Cache key 'FOO' exists under multiple sites: <a>, <b>. Pass site to disambiguate.`

#### Scenario: Complete and fresh cache
- **WHEN** the cache has all eight sections, `lastUpdated` is now, and `maxAgeHours` is `24`
- **THEN** `exists` is `true`, `fresh` is `true`, and `missing` is empty

#### Scenario: Stale cache
- **WHEN** `lastUpdated` is 48 hours ago and `maxAgeHours` is `24`
- **THEN** `exists` is `true` and `fresh` is `false`

#### Scenario: Invalid cache JSON
- **WHEN** the cache file is not valid JSON
- **THEN** `exists` is `true`, `fresh` is `false`, `missing` is `["all"]`
- **AND** `errors` has `Cache file is not valid JSON: <cause>`

#### Scenario: Unreadable project config
- **WHEN** reading the `[jira]` config section fails (for example a legacy `.claude/sdlc.json` marker exists) and `skipConfigCheck` is `false`
- **THEN** the result has `exists` `false`, `missing` `["all"]`, and the read error in `errors`
- **AND** with `skipConfigCheck` `true` the config is treated as empty and the check runs

### Requirement: check enforces jira.projects membership
When `jira.projects` in `.sdlc-v2/config.toml` lists 2 or more keys, the `check` action SHALL reject a key outside the list with a `DomainError`. No other action checks membership.

#### Scenario: Key outside jira.projects
- **WHEN** `jira.projects` is `["FOO", "BAR"]` and `check` runs with `key` `BAZ`
- **THEN** the tool returns `DomainError` `Project BAZ is not in jira.projects: [FOO, BAR]`
- **AND** the suggestion says to add the key to `jira.projects` in `.sdlc-v2/config.toml` or pass a listed key

#### Scenario: Single-entry list is not enforced
- **WHEN** `jira.projects` has one entry
- **THEN** `check` does not check membership

### Requirement: check-default-project reads jira.defaultProject
The `check-default-project` action SHALL return `ok` `true`, `defaultProject`, and `next`, and SHALL never return an error.

| Field | Meaning |
|---|---|
| `ok` | always `true` |
| `defaultProject` | `jira.defaultProject` from `.sdlc-v2/config.toml`; `""` when unset, not a string, or config unreadable |
| `next` | `Use defaultProject if non-empty; otherwise continue the ordered project-key fallback (AskUserQuestion).` |

#### Scenario: Default project set
- **WHEN** `.sdlc-v2/config.toml` has `[jira]` `defaultProject = "FOO"`
- **THEN** `defaultProject` is `FOO`

#### Scenario: No config
- **WHEN** no `[jira]` section exists
- **THEN** `defaultProject` is `""` and `next` is non-empty

#### Scenario: Wrong type
- **WHEN** `defaultProject` is an array
- **THEN** `defaultProject` is `""` and no error is returned

### Requirement: load returns the cache object
The `load` action SHALL return the cache file content as-is.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| key cached under 2+ sites, no `site` | `DomainError` | `multiple cache entries for '<KEY>' — pass site to disambiguate` / call `check` for `candidateSites` |
| no cache file | `DataError` | `no cache found for project; run cache initialization first` / call `save` first |
| file is not valid JSON | `DataError` | `cache file is not valid JSON: <cause>` / fix or delete, then `save` |

#### Scenario: Save then load
- **WHEN** `save` wrote `cloudId` `cloud-1` for `FOO` and `load` runs with the same `cacheDir`
- **THEN** the result has `cloudId` `cloud-1`

#### Scenario: Missing cache
- **WHEN** `load` runs for a key with no cache file
- **THEN** the tool returns `DataError` `no cache found for project; run cache initialization first`

### Requirement: save writes the whole cache file
The `save` action SHALL require `data` to contain the keys `version`, `cloudId`, `project`, and `siteUrl`, and SHALL atomically replace the cache file with `data`.

- With `cacheDir`: path is `<cacheDir>/<KEY>.json`.
- Without `cacheDir`: path is `~/.sdlc-cache/jira/<site>/<KEY>.json`, where `<site>` is the `site` input or the `data.siteUrl` host lowercased with `.` replaced by `_` (`https://acme.atlassian.net` becomes `acme_atlassian_net`).
- Output: `saved` `true`, `cachePath`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| required keys absent | `DomainError` | `cache JSON is missing required fields: <list>` / add them to `data` |
| no `site` and no host in `data.siteUrl` | `DomainError` | `cannot derive site host from siteUrl: <value>` / set a full site URL |
| directory or file write fails | `InfraError` | `create cache dir: <cause>` or `write cache file: <cause>` |

#### Scenario: Missing required fields
- **WHEN** `save` runs with `data` `{"version": 1}`
- **THEN** the tool returns `DomainError` `cache JSON is missing required fields: cloudId, project, siteUrl`

#### Scenario: Home layout path
- **WHEN** `save` runs for `FOO` without `cacheDir` or `site` and `data.siteUrl` is `https://acme.atlassian.net`
- **THEN** the file is written at `~/.sdlc-cache/jira/acme_atlassian_net/FOO.json`

### Requirement: save-field merges one cache key
The `save-field` action SHALL require `fieldName` and `data`, then SHALL shallow-merge `data` into the cache key `fieldName` when the existing value is a JSON object, or overwrite it otherwise.

- On merge, keys in `data` win over existing keys.
- The file is written atomically. Output: `saved` `true`, `field`, `cachePath`.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `fieldName` empty | `DomainError` | `fieldName is required for save-field` |
| `data` absent | `DomainError` | `data is required for save-field` / pass at least `{}` |
| no cache file, or key cached under 2+ sites | `DataError` | `no cache found for project; run cache initialization first` / call `save` first |
| file is not valid JSON | `DataError` | `cache file is not valid JSON: <cause>` |

#### Scenario: Merge into an object
- **WHEN** the cache has `issueTypes` `{"Task": {...}}` and `save-field` runs with `fieldName` `issueTypes` and `data` `{"Bug": {...}}`
- **THEN** the cached `issueTypes` holds both `Task` and `Bug`

#### Scenario: Overwrite a non-object
- **WHEN** the cache has `linkTypes` `["blocks"]` and `save-field` runs with `fieldName` `linkTypes` and `data` `{"replaced": true}`
- **THEN** the cached `linkTypes` is `{"replaced": true}`

#### Scenario: Data missing
- **WHEN** `save-field` runs without `data`
- **THEN** the tool returns `DomainError` `data is required for save-field`

### Requirement: Default templates directory discovery
The tool SHALL resolve the default templates directory as the first match in this order:

| # | Source | Used when |
|---|---|---|
| 1 | `templatesDir` input | set |
| 2 | `$CLAUDE_PLUGIN_ROOT/skills/jira/templates` | `CLAUDE_PLUGIN_ROOT` is set and the directory exists |
| 3 | first directory named `templates` whose parent is named `jira` under `~/.claude/plugins` (searched up to 6 levels deep, once per server process) | one is found |
| 4 | `<cwd>/plugins/sdlc/skills/jira/templates` | always (last resort) |

- A missing directory is not an error: it means no default templates.

#### Scenario: Override
- **WHEN** `templatesDir` is set
- **THEN** default templates are read only from that directory

#### Scenario: Plugin root
- **WHEN** `templatesDir` is empty and `CLAUDE_PLUGIN_ROOT` points at a directory with `skills/jira/templates/Task.md`
- **THEN** `templates` resolves `Task` to `default`

#### Scenario: Working-directory fallback
- **WHEN** `templatesDir` and `CLAUDE_PLUGIN_ROOT` are empty, `~/.claude/plugins` has no `jira/templates`, and the working directory has `plugins/sdlc/skills/jira/templates/Task.md`
- **THEN** `templates` resolves `Task` to `default`

#### Scenario: Plugin root without templates
- **WHEN** `CLAUDE_PLUGIN_ROOT` is set but has no `skills/jira/templates` directory
- **THEN** discovery continues with the `~/.claude/plugins` search and then the working-directory fallback

### Requirement: templates reports per-type template resolution
The `templates` action SHALL resolve a template source for every issue type in the cached `issueTypes`, in the order custom, default, default-fallback, none.

| Status | Condition |
|---|---|
| `custom` | `.sdlc-v2/jira-templates/<Type>.md` exists |
| `default` | `<templatesDir>/<Type>.md` exists |
| `default-fallback` | type is `Sub-bug` (to `Bug`), `Sub-task` (to `Task`), or `Subtask` (to `Task`) and the parent default exists |
| `none` | none of the above |

| Field | Meaning |
|---|---|
| `issueTypes` | cached issue-type names, sorted |
| `customTemplates` | per type: `path`, `exists` |
| `defaultTemplates` | names of `*.md` files in the templates directory |
| `resolved` | per type: status from the table above |
| `fallbacks` | `[{type, fallbackTo}]` for `default-fallback` types |
| `noneTypes` | types with status `none` |

- A missing or unreadable cache yields empty lists, not an error.

#### Scenario: Mixed resolution
- **WHEN** the cache has `Task`, `Sub-task`, `Epic`, the templates dir has only `Task.md`, and `.sdlc-v2/jira-templates/Epic.md` exists
- **THEN** `resolved` is `Task`: `default`, `Sub-task`: `default-fallback`, `Epic`: `custom`

### Requirement: init-templates copies exact-match defaults
The `init-templates` action SHALL create `.sdlc-v2/jira-templates/` and, for each cached issue type, copy `<templatesDir>/<Type>.md` to `.sdlc-v2/jira-templates/<Type>.md` only when the destination does not exist.

- Output: `initialized` (copied), `skipped` (destination existed), `unavailable` (no exact-name default; the fallback map is not used).
- Copy or directory failures return `InfraError` (`copy template: <cause>`, `create custom templates dir: <cause>`).

#### Scenario: First run
- **WHEN** the cache has `Task` and `Epic` and only `Task.md` is a default
- **THEN** `initialized` is `["Task"]`, `unavailable` is `["Epic"]`, and `.sdlc-v2/jira-templates/Task.md` exists

#### Scenario: Rerun
- **WHEN** `init-templates` runs again
- **THEN** `skipped` is `["Task"]` and the existing file is not overwritten

### Requirement: clear deletes cache files
The `clear` action SHALL delete the project cache file(s) and SHALL return `cleared` `true` even when no file existed.

- With `cacheDir`: deletes `<cacheDir>/<KEY>.json`; output has `cachePath`.
- Without `cacheDir`: deletes `<KEY>.json` under every home-cache site dir, or only under `site` when set; output has `cachePaths` (files actually deleted).
- A delete failure returns `InfraError` `delete cache file: <cause>`.

#### Scenario: Clear with cacheDir
- **WHEN** `<cacheDir>/FOO.json` exists and `clear` runs for `FOO`
- **THEN** `cleared` is `true` and the file no longer exists

### Requirement: copy-template never overwrites
The `copy-template` action SHALL copy `<templatesDir>/<templateFrom>.md` to `.sdlc-v2/jira-templates/<templateType>.md` and SHALL NOT overwrite an existing destination.

| Outcome | Output |
|---|---|
| copied | `copied` `true`, `type`, `from`, `destination` |
| destination exists | `copied` `false`, `reason` `exists`, `type`, `destination` |

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `templateType` or `templateFrom` empty | `DomainError` | `templateType and templateFrom are required for copy-template` |
| source file missing | `DataError` | `template source not found: <path>` / call `templates` for names |
| directory or copy fails | `InfraError` | `create custom templates dir: <cause>` or `copy template: <cause>` |

#### Scenario: Copy then repeat
- **WHEN** `copy-template` runs with `templateType` `Sub-bug` and `templateFrom` `Task` twice
- **THEN** the first call returns `copied` `true` and writes `.sdlc-v2/jira-templates/Sub-bug.md`
- **AND** the second call returns `copied` `false` with `reason` `exists`

#### Scenario: Missing source
- **WHEN** `templateFrom` is `DoesNotExist`
- **THEN** the tool returns `DataError` `template source not found: <templatesDir>/DoesNotExist.md`

### Requirement: validate-body link check
The `validate-body` action SHALL extract every `http://` or `https://` URL from `markdownBody`, validate each by URL class, and SHALL set `ok` to `false` when any URL has a violation.

- A URL ends at whitespace or any of `) ] > " '`; trailing `.,;:!?` are stripped.
- Duplicate URLs are checked once; `line` is the 1-based line of the first occurrence.
- Offline mode is on when the server process has `SDLC_LINKS_OFFLINE=1`; there is no per-call input for it.
- `site` has no effect. `cacheDir` replaces `~/.sdlc-cache/jira` as the root scanned for the cached Jira site.
- Per-class rules match the `links_validate` tool (see `openspec/specs/tool-links-validate/spec.md`); the table is a summary.

| URL class | Check | Violation `reason` | Skipped `reason` |
|---|---|---|---|
| `github.com/<owner>/<repo>/(issues\|pull)/<n>` | when the `origin` remote of the main root resolves, owner/repo must match it (case-insensitive); existence via `gh issue view` / `gh pr view` unless offline | `github-context-mismatch`, `github-not-found` | — |
| `*.atlassian.net/browse/<KEY-N>` | host equals the single site dir under the cache root (`_` read as `.`) | `atlassian-site-mismatch` (also when no site is cached), `atlassian-site-ambiguous` (2+ site dirs) | — |
| any other URL | `linkedin.com`, `x.com`, `twitter.com`, `medium.com` (and `www.`) are skipped; offline skips; else HEAD (GET on 405/501), 5 s timeout | `url-unreachable`, `url-not-found` (4xx), `url-server-error` (5xx) | `skip-list`, `offline` |
| unparseable URL | — | `url-invalid` | — |

| Field | Meaning |
|---|---|
| `ok` | `true` when there are no violations |
| `violations` | `[{url, line, reason, detail}]` |
| `skipped` | `[{url, line, reason}]` |
| `message` | only when `ok` is `false`: starts `Link verification failed:`, one line per violation, ends `Remove or correct the listed URLs and retry.` |
| `adf` | only when `markdownBody` is non-empty: its conversion to an ADF v1 document |

#### Scenario: Offline generic URL
- **WHEN** offline mode is on and `markdownBody` is `See https://example.com/docs for details.`
- **THEN** `ok` is `true` and `skipped` has one entry with `reason` `offline`
- **AND** `adf` is present

#### Scenario: Empty body
- **WHEN** `markdownBody` is empty
- **THEN** `ok` is `true` and `adf` is absent

#### Scenario: Atlassian site match
- **WHEN** `cacheDir` has one site dir `example_atlassian_net` and the body links `https://example.atlassian.net/browse/FOO-1`
- **THEN** `ok` is `true`

#### Scenario: Atlassian site mismatch
- **WHEN** `cacheDir` has one site dir `other_atlassian_net` and the body links `https://example.atlassian.net/browse/FOO-1`
- **THEN** `ok` is `false` with one violation `atlassian-site-mismatch`
- **AND** `message` is non-empty

#### Scenario: Atlassian sites ambiguous
- **WHEN** `cacheDir` has two site dirs and the body links an `atlassian.net/browse/` URL
- **THEN** `ok` is `false` with one violation `atlassian-site-ambiguous`

#### Scenario: Line tracking
- **WHEN** the body has `https://example.com/a` on line 2 and `https://example.com/b` on line 4
- **THEN** the entries report `line` `2` and `line` `4`

#### Scenario: Dedupe and punctuation
- **WHEN** the body is `See https://example.com/a, and also https://example.com/a. Also (https://example.com/b).`
- **THEN** exactly two URLs are checked: `https://example.com/a` and `https://example.com/b`

### Requirement: validate-body converts markdown to ADF
When `markdownBody` is non-empty, the `validate-body` action SHALL return its Atlassian Document Format v1 conversion in `adf`.

- Supported: headings h1–h3, paragraphs, bold, italic, inline code, fenced code blocks, bullet and ordered lists, links, tables, blockquotes, horizontal rules.
- Unrecognized markdown becomes plain-text paragraph nodes; it does not fail.

#### Scenario: ADF returned with violations
- **WHEN** `markdownBody` is non-empty and has a link violation
- **THEN** the result has `ok` `false` and still includes `adf`

### Requirement: write-critique stores the critique artifact
The `write-critique` action SHALL validate `hash`, require `data`, and atomically write `data` as JSON to `.sdlc-v2/state/artifacts/critique-<hash>.json` under the main root.

- Output: `saved` `true`, `hash`, `next` `Critique artifact written — present Initial:/Critique:/Final: to the user, then proceed to Step 2.6 approval.`
- An existing file with the same hash is replaced.

| Condition | Class | Message / Suggestion (short) |
|---|---|---|
| `hash` empty | `DomainError` | `hash is required` / compute via `sha256sum \| cut -c1-12` |
| `hash` not `^[a-zA-Z0-9]+$` | `DomainError` | `invalid hash "<hash>": must be alphanumeric only` |
| `data` absent | `DomainError` | `data is required for write-critique` |
| dir or file write fails | `InfraError` | `create artifacts dir: <cause>` or `write critique artifact: <cause>` |

#### Scenario: Happy path
- **WHEN** `write-critique` runs with `hash` `abc123` and `data` `{"initial": "a", "findings": "b", "final": "c"}`
- **THEN** `saved` is `true`
- **AND** `.sdlc-v2/state/artifacts/critique-abc123.json` holds that JSON

#### Scenario: Path-like hash
- **WHEN** `hash` is `../escape`, `has/slash`, `has space`, or empty
- **THEN** the tool returns a `DomainError` and writes no file

#### Scenario: Missing data
- **WHEN** `write-critique` runs with a valid `hash` and no `data`
- **THEN** the tool returns `DomainError` `data is required for write-critique`

### Requirement: write-approval stores the approval token
The `write-approval` action SHALL validate `hash` like `write-critique` and write a token file at `.sdlc-v2/state/artifacts/approval-<hash>.token` under the main root.

- Only the file's existence is the contract; its content is not.
- Output: `saved` `true`, `hash`, `next` `Approval token written — proceed to Step 3 dispatch.`
- Write failures return `InfraError` (`create artifacts dir: <cause>`, `write approval token: <cause>`).

#### Scenario: Happy path
- **WHEN** `write-approval` runs with `hash` `abc123`
- **THEN** `saved` is `true` and `.sdlc-v2/state/artifacts/approval-abc123.token` exists

#### Scenario: Invalid hash
- **WHEN** `hash` is `has/slash`
- **THEN** the tool returns a `DomainError` and writes no file

### Requirement: Tool annotations
The tool SHALL register with title `Manage local Jira cache` and annotations `ReadOnly` `false`, `Destructive` `true`, `Idempotent` `false`, `OpenWorld` `true`. `OpenWorld` is `true` because `validate-body` sends HTTP requests to URLs in the body and runs `gh issue view` / `gh pr view` unless `SDLC_LINKS_OFFLINE=1`.

#### Scenario: Registration
- **WHEN** the MCP server lists its tools
- **THEN** `jira` appears with the title and annotations above
