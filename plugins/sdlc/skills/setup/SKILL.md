---
name: setup
description: "Use this skill when setting up the SDLC plugin for a project, initializing configuration, or when any skill reports missing config. Renders a selective-section menu so users choose which sections to configure; each selected section prints a verbose header (purpose, files-modified, consuming skills, per-option description) before any prompt. Supports direct sub-flow entry via --only, --dimensions, --pr-template, --guardrails, --execution-guardrails, --openspec-enrich, --plan-template. Arguments: [--migrate] [--skip <section>] [--force] [--only <ids>] [--dimensions] [--pr-template] [--guardrails] [--execution-guardrails] [--openspec-enrich] [--remove-openspec] [--plan-template] [--add] [--no-copilot]"
user-invocable: true
argument-hint: "[--migrate] [--skip <section>] [--force] [--only <ids>] [--dimensions] [--pr-template] [--guardrails] [--execution-guardrails] [--openspec-enrich] [--remove-openspec] [--plan-template] [--add] [--no-copilot]"
model: sonnet
---

# SDLC Setup

Unified setup skill that replaces the fragmented first-use experience. Detects existing
configuration, migrates legacy files, walks the user through missing sections, and
delegates content creation to specialized skills.

**Announce at start:** "I'm using setup (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

---

## Port Notes (Task 44 — read before using this skill)

This is a Go/MCP port of the original script-driven skill. `internal/tools/setup.go`'s
`setup_prepare` tool returns only static per-section descriptors (`id`, `label`, `purpose`,
`configFile`, `configPath`, `consumedBy`, `filesModified`, `optional`, `delegatedTo`,
`confirmDetected`, `fields[]`) plus `needsMigration` (a single project-wide boolean),
`defaultBranch`, and `remoteOwner`. It has no equivalent of source's `state`/`summary`/
`locked` per row, nor of `projectConfig`/`localConfig`/`legacy`/`openspecConfig`/`content`/
`detected.versionFile-fileType-tagPrefix`/`preReleaseCompat`/`menuInputContract`/
`scriptVersions`. Where the source procedure depended on one of those, this port computes
it LLM-natively (Read/Glob/Grep, inline static tables) rather than depending on a tool
field that does not exist. Deviations, one line each:

- **Q1 (binding ruling):** the `workspace` and `hooks` menu sections (source's 3.workspace
  / 3.hooks, issues #351/#370/#372) are dropped entirely. Go's `internal/setupmeta.Sections()`
  is a frozen 16-id manifest with no `workspace`/`hooks` id — there is nothing to dispatch to.
  `--skip`/`--only` no longer accept those ids.
- **State/summary/locked (Gap A):** Step 0/1 below compute `state` and `summary` per row by
  reading `.sdlc-v2/config.json` / `.sdlc-v2/local.json` directly and Globbing the content
  markers, reproducing `scripts/lib/setup-sections.js`'s `computeState`/`summarize*`
  functions verbatim (see Step 1). The `[legacy]` per-row badge and the "locked,
  always-included" menu rule are dropped — Go's `needsMigration` has no per-section
  attribution to key a per-row legacy state off of. Step 1 prints one migration banner
  instead, and Step 2 still runs before the menu answers are acted on.
- **Misplaced-section detection:** source additionally flagged a section `legacy` when a key
  was nested at the wrong config-file top level (e.g. `ship` under `.sdlc-v2/config.json`). No
  Go equivalent exists; dropped. The legacy-file markers checked in Step 0 still catch every
  concrete legacy layout `internal/configmigrate` knows how to migrate.
- **`preReleaseCompat` (Gap B):** inlined as a static 6-row table in 3.G below, copied
  verbatim from source's `PRE_RELEASE_COMPAT` constant (`scripts/skill/setup.js`) — no Go
  tool field carries it.
- **Diff preview (Gap C):** source's `computeConfigDiff` pure-JS diff has no Go/tool
  equivalent. Replaced with an LLM-native before/after table built from the Step 0 snapshot
  vs. the values assembled during Step 3 (see "Diff preview" below).
- **R-SCRIPT-VERSIONS warning dropped.** No Go tool surfaces installed-vs-current CI script
  versions; there is nothing to compare. Step 0 has no equivalent warning.
- **`setup_init` vs `setup_write_sections`:** `setup_init` (existing Go tool) only ever seeds
  *empty* `{}` objects for the ids it is given — it cannot accept assembled field values the
  way source's `util/setup-init.js --project-config ... --local-config ...` did. This port
  calls `setup_init({ sections: [] })` once, near Step 0, purely to scaffold `.sdlc-v2/`, both
  managed `.gitignore` blocks, and empty `config.json`/`local.json` — never with real ids
  (passing real ids would seed spurious empty top-level keys that then need to be
  overwritten). All real field values collected in Step 3 are written via the additive
  `setup_write_sections` tool instead (see "Writing config files").
- **`setup_write_sections` keys are config-file top-level keys, not section ids.** The JSON
  key passed to `setup_write_sections` is the segment of `section.configPath` before its
  first `.` — e.g. `received-review`'s `configPath` is `receivedReview` (camelCase, no
  hyphen) → write under key `receivedReview`; `plan-guardrails`'s `configPath` is
  `plan.guardrails` → write under key `plan` as `{ guardrails: [...] }`. Never use
  `section.id` verbatim as the write key.
- **`pr` wholesale-write hazard.** `pr-labels` (`setup-pr-labels.md`) and this file's own
  3.pr both write into the same top-level `pr` config key. `setup_write_sections` /
  `config.WriteSection` replace a key wholesale, so "Writing config files" below always
  re-reads the current `pr` object immediately before writing `pr` and preserves any
  `labels` key already present.
- **`--only`/`--skip` id list corrected.** Source's own SKILL.md listed 13 ids for `--only`
  (missing `received-review`). The table below lists the true 16 canonical ids from
  `internal/setupmeta.Sections()` (includes `plan-style` and `plan-tasks`, added after the
  original port to expose `/plan`'s narrative-style and task-contract config).
- **Delete-legacy-files prompt retained.** `migrate({ action: "config" })`'s `Result` string
  names every ingested legacy path inline (e.g. `"...legacy ingested: [.claude/sdlc.json
  .claude/version.json]"`) — `MigrateOut` has no dedicated array field for them, so Step 2
  parses the bracketed list out of the `Result` string rather than reading a structured
  field.
- **Tool map:** `setup_prepare` (Step 0 descriptors), `setup_init` (Step 0, scaffold-only),
  `migrate` (Step 2), `setup_write_sections` (Step 3 "Writing config files"). `validate` is
  called only inside the delegated companion sub-flows (`setup-dimensions.md`,
  `setup-pr-template.md`, `setup-guardrails.md`, `setup-execution-guardrails.md`), never
  directly by this file.

---

## Arguments

| Flag | Description | Default |
|------|-------------|---------|
| `--migrate` | Force migration of legacy config files even if no legacy files are auto-detected | off |
| `--skip <section>` | Skip a config section during setup. Valid values: any of the 16 canonical ids — `version`, `ship`, `jira`, `review`, `received-review`, `commit`, `pr`, `pr-labels`, `review-dimensions`, `pr-template`, `plan-template`, `plan-style`, `plan-tasks`, `plan-guardrails`, `execution-guardrails`, `openspec-block` | none |
| `--force` | Pre-check every menu row (reconfigure everything) instead of selecting only `not-set` rows | off |
| `--only <ids>` | Comma-separated section ids to configure non-interactively (skips the menu). Same 16 ids as `--skip` above | none |
| `--dimensions` | Jump directly to review dimensions sub-flow (alias for `--only review-dimensions`) | off |
| `--pr-template` | Jump directly to PR template sub-flow (skip config builder) | off |
| `--guardrails` | Jump directly to plan guardrails sub-flow (skip config builder) | off |
| `--execution-guardrails` | Jump directly to execution guardrails sub-flow (skip config builder) | off |
| `--openspec-enrich` | Jump directly to openspec config enrichment sub-flow | off |
| `--remove-openspec` | Remove the managed block from openspec/config.yaml (with --openspec-enrich) | off |
| `--plan-template` | Jump directly to plan template sub-flow (alias for `--only plan-template`) | off |
| `--add` | Expansion mode (with --dimensions or --guardrails or --execution-guardrails) | off |
| `--no-copilot` | Skip GitHub Copilot instructions (with --dimensions) | off |

---

## Plan Mode Check

If the system context contains "Plan mode is active":

1. Announce: "This skill requires write operations. Exit plan mode first, then re-invoke `/setup`."
2. Stop. Do not proceed to subsequent steps.

---

## Workflow

### Step 0 — Pre-flight

1. Call the `setup_prepare` MCP tool:

   ```
   setup_prepare({ skipConfigCheck: false }) → { ok, needsMigration, sections[], defaultBranch, remoteOwner }
   ```

   `sections[]` is the static 16-row descriptor list, always in canonical
   `internal/setupmeta.Sections()` order: `version`, `ship`, `jira`, `review`,
   `received-review`, `commit`, `pr`, `pr-labels`, `review-dimensions`, `pr-template`,
   `plan-template`, `plan-style`, `plan-tasks`, `plan-guardrails`, `execution-guardrails`,
   `openspec-block`. Each row carries `{ id, label, purpose, configFile, configPath,
   consumedBy, filesModified, optional, delegatedTo, confirmDetected, fields[] }`.

2. Call `setup_init({ sections: [] })` once, unconditionally, to scaffold `.sdlc-v2/` (see Port
   Notes — this never carries real ids):

   ```
   setup_init({ sections: [] }) → { ok, created[], changed[] }
   ```

   This ensures `.sdlc-v2/.gitignore`, the root `.gitignore` managed block, and empty
   `.sdlc-v2/config.json` / `.sdlc-v2/local.json` all exist before any read below.

3. **Snapshot current config** (cache for the rest of this run — do not re-read mid-run
   unless Step 2 migration or a write changes the files):
   - Read `.sdlc-v2/config.json` → `projectConfig` (absent file = `{}`, not an error).
   - Read `.sdlc-v2/local.json` → `localConfig` (absent file = `{}`).
   - Glob `.sdlc-v2/review-dimensions/*.yaml` → dimension count.
   - Glob `.sdlc-v2/pr-template.md` and `.sdlc-v2/plan-template.md` → existence booleans.
   - If `openspec/config.yaml` exists, Read it and search for a line matching
     `# BEGIN MANAGED BY sdlc-utilities (v<N>)`; capture `<N>` as the managed-block version
     (no match, or file absent → no managed block).

4. **Legacy-file detection** (mirrors `internal/configmigrate`'s `legacyMarkers` exactly —
   Glob each path relative to the project root): `.claude/sdlc.json`, `.claude/version.json`,
   `.sdlc-v2/jira-config.json`, `.sdlc-v2/ship-config.json`, `.sdlc-v2/review.json`,
   `.claude/review.json`. Also Glob `.claude/jira-templates/` separately — it drives
   `migrate({ action: "import" })` in Step 2 but is not one of the six markers
   `needsMigration` is based on.

5. **Version detection** (source's `detected.versionFile`/`fileType`/`tagPrefix` — no Go
   field carries this): Glob in this priority order — `package.json`, `Cargo.toml`,
   `pyproject.toml`, `pubspec.yaml`, `plugin.json` — first match sets `detected.versionFile`
   and the matching `fileType` enum value. Run `git tag --list` (Bash); if any tags exist,
   take the common leading non-digit substring of the most recent few tags as
   `detected.tagPrefix`; default to `v` when there are no tags or no consistent prefix.

6. **Flag routing** (unchanged from source). The direct-entry flags map onto `--only`, which
   drives Step 3 directly:

   | Flag passed | Equivalent `--only <id>` |
   |---|---|
   | `--dimensions` | `--only review-dimensions` |
   | `--pr-template` | `--only pr-template` |
   | `--guardrails` | `--only plan-guardrails` |
   | `--execution-guardrails` | `--only execution-guardrails` |
   | `--openspec-enrich` | `--only openspec-block` |
   | `--plan-template` | `--only plan-template` |

   If any of those flags is passed (and `--only` is not), translate it into `--only <id>`. If
   `--only <ids>` is passed (directly or via translation), skip Step 1's menu and proceed to
   Step 2 → Step 3 with `selectedIds = <ids>`. Pass through `--add`, `--no-copilot`, and
   `--remove-openspec` to the relevant sub-flow when invoked.

   If none of the direct-entry flags or `--only` were passed: continue with the full
   interactive flow (Steps 1 → 2 → 3 → 4).

---

### Step 1 — Selective-Section Menu

<!-- Implements R-menu-1, R-menu-4. Fixes #337. Step 1 is plain chat output; AskUserQuestion is intentionally NOT used here. -->

**Direct-entry flag bypass (preserved):** When `--only`, `--force`, `--dimensions`,
`--pr-template`, `--guardrails`, `--execution-guardrails`, `--openspec-enrich`, or
`--plan-template` was passed, `selectedIds` are resolved before Step 1 by the flag-alias
routing table in Step 0. Skip the entire menu (no numbered list, no chat prompt) and jump to
Step 2/3 with the resolved id set.

**Compute `state` per row** (evaluate in this order; ported verbatim from
`scripts/lib/setup-sections.js`'s `computeState`, minus the dropped `legacy`/misplaced-section
branches — see Port Notes):

1. **Content/delegated sections** (`review-dimensions`, `pr-template`, `plan-template`,
   `openspec-block`): `set` when the Step 0 existence check found content (dimension count >
   0 / file exists / managed block found), else `not-set`.
2. **`.sdlc-v2/config.json` sections** (`configFile === '.sdlc-v2/config.json'`): walk
   `section.configPath` as a dot-path into `projectConfig` (e.g. `plan.guardrails`,
   `pr.labels`). If the resolved value is an array, `set` requires `length > 0`; any other
   non-null resolved value is `set`. Unresolved (any segment missing) → `not-set`.
3. **`.sdlc-v2/local.json` sections** (`ship`, `review`, `received-review`, `plan-style`): `set`
   when `localConfig[section.configPath]` (e.g. `localConfig.receivedReview`,
   `localConfig.planStyle`) is non-null, else `not-set`.

If `needsMigration` is `true`, print one banner line above the status block (no per-row
`[legacy]` badge — Go's `needsMigration` carries no per-section attribution):

```
⚠ Legacy or outdated config detected — see Step 2 (migration) before configuring sections below.
```

**Compute `summary` per row** (ported verbatim from `scripts/lib/setup-sections.js`'s
`summarize*` functions; "join non-empty" means: build each listed piece only when its
source value is present/non-empty, then join the resulting pieces with `, ` unless noted
otherwise):

| id | Summary rule |
|---|---|
| `version` | No config: `detected: <versionFile> (<fileType>), tag: <tagPrefix>` if detected, else empty. With config: join non-empty of `file: <versionFile>` (when `mode=file`), `mode: tag` (when `mode=tag`), `tag: <tagPrefix>`, `pre: <preRelease>`. |
| `ship` | Join non-empty (joined with two spaces) of `steps: <a,b,c>`, `bump: <bump>`, and any present R57 tunable rendered as `<field>: <value>` (`verifyPipelineTimeout`, `verifyPipelineInterval`, `verifyPipelineMaxIterations`, `awaitRemoteReviewTimeout`, `awaitRemoteReviewInterval`, `awaitRemoteReviewers` joined `a,b`). |
| `jira` | `project: <defaultProject>` or empty. |
| `review` | `scope: <scope>` or empty. |
| `received-review` | `auto-apply: <a,b>` (joined `alwaysFixSeverities`) or empty. |
| `commit` | Join non-empty (two spaces) of `pattern: <subjectPattern>` (truncate to 37 chars + `...` if over 40) and `types: <allowedTypes.length>`. |
| `pr` | Join non-empty (two spaces) of `defaultBranch: <defaultBranch>` and `pattern: <titlePattern>` (same 40-char truncation). Does **not** include `labels` — see the `pr-labels` row. |
| `pr-labels` | From `pr.labels.mode`: `off` → `off — no automatic labels`; `rules` → `rules: <N> rule(s)`; `llm` → `llm — model picks labels`; absent → empty. |
| `review-dimensions` | `<count> installed` or empty. |
| `pr-template` | `installed` or empty. |
| `plan-template` | `installed` or empty. |
| `plan-style` | Join non-empty (two spaces) of `verbosity: <verbosity>`, `audience: <audience>`, `rules: <narrativeRules.length>` (only when > 0). |
| `plan-tasks` | Join non-empty (two spaces) of `contract: <contractShape>` and `required: <requiredFields.length>` (only when > 0). |
| `plan-guardrails` | `<N> configured` (array length) or empty. |
| `execution-guardrails` | `<N> configured` (array length) or empty. |
| `openspec-block` | `managed-block v<N>` when a block was found, else empty. |

**Phase 1 — Render the status block.** Print the status block using `section.label` and the
computed `summary` verbatim:

**State badge per row** (driven by the computed `state`):
- `[set]` — section is already configured.
- `[not set]` — section has no config.

**Layout:**

```
SDLC Setup
---------------------------------------------------
Detected configuration:

  [set]      <id>            <summary>
  [not set]  <id>            <summary or "—">
  ...
```

**Phase 2 — Print the numbered menu directly to chat.** One line per row in the canonical
16-id order, format:

```
<N>. [<state>] <section.label> — <first sentence of section.purpose>
```

- N is 1-indexed, assigned in array order.
- `<state>` mirrors the badge: `set` | `not-set`.
- All strings MUST come from the manifest (`internal/setupmeta.Sections()` /
  `setup_prepare`'s output); do NOT hardcode labels or descriptions.

Example rendering:
```
1. [set] Version — Tells /pr and /ship where the canonical version string lives.
2. [not-set] Ship — Developer-local pipeline preferences for /ship.
3. [not-set] Review dimensions — Review dimensions installed under .sdlc-v2/review-dimensions/*.yaml.
4. [not-set] Plan template — Project-owned plan template at .sdlc-v2/plan-template.md.
```

**Phase 3 — Ask via plain chat (NOT `AskUserQuestion`).** Print the following prompt as a
literal chat message, then end the model turn so the user's next message is the answer:

```
Reply with the numbers to configure (e.g. 1,3,5 or 1-3,7), or type:
  all       — configure every section
  not-set   — configure only sections currently [not set]
  none      — exit without changes
  cancel    — exit without changes (alias for none)
Default: all
```

Do NOT wrap this in `AskUserQuestion`. It is a literal chat output followed by a turn
boundary. (Source's default token could flip to `not-set` behind an internal `--unset-only`
flag; that flag was never exposed in source's own SKILL.md argument surface, so this port's
default is always `all`.)

**Phase 4 — Parse the reply**:
- Empty reply → `all`.
- `all` → every id from the 16-id canonical list.
- `not-set` → ids whose computed `state === 'not-set'`.
- `none` or `cancel` → empty list → print `No sections selected — no changes made.` and jump
  to Step 4.
- Comma- or space-separated numbers, optionally including `M-N` ranges → resolve each token
  to a row by 1-indexed position; union the results.
- **Invalid input:** if any token is unknown or out of range, print one line:
  `Invalid input: "<token>" is not a number, range, or known keyword. Try again.` Then
  re-print the numbered list and the prompt; wait for a new reply. Maximum 3 retries; after
  that, exit with `No valid input after 3 attempts — no changes made.`

Store the resolved section ids as `selectedIds`. Defer migration and field collection to
Step 2 / Step 3.

---

### Step 2 — Migration

**Skip this step if:** `needsMigration` is `false` AND `--migrate` was NOT passed.

If any of the six legacy markers from Step 0 exist, or `--migrate` was passed, use
AskUserQuestion:

> Legacy or outdated config files detected. Migrate to the current config format before
> proceeding?

Options:
- **yes** — migrate now (recommended)
- **no** — configure from scratch (legacy files are left untouched)

On **yes**, run the config migration:

```
migrate({ action: "config", dryRun: false }) → { ok, result, changed[] }
```

`result` is one of: `"up-to-date"` (nothing to do), or
`"migrated (steps: [...], legacy ingested: [<path> <path> ...])"`. This single call migrates
both `.sdlc-v2/config.json` and `.sdlc-v2/local.json` schema versions and ingests any of the six
legacy per-section files found in Step 0 — there is no separate project/local/`--unset-only`
branch to run.

Then always run (the tool itself no-ops per-file/dir when a legacy source is absent, so this
is safe even when no legacy `.sdlc/` content exists):

```
migrate({ action: "import", dryRun: false }) → { ok, result, changed[] }
```

This non-destructively copies config.json, local.json, templates, jira-templates, learnings,
and review-dimensions from the old data directory into `.sdlc-v2/`. `config.json` and
`local.json` merge per top-level key — a key the new file already holds is never overwritten,
but a key present only in the legacy file is added even when the new file already exists
(e.g. setup's own empty-`{}` scaffold). Everything else (`pr-template.md`, `plan-template.md`,
`jira-templates/`, `learnings/`, `review-dimensions/`) is skipped whole-file/whole-dir when
the destination already exists. `result` is either `"up-to-date: nothing to import"` or
`"imported: [<path> <path> ...]"` (or `"would-import: [...]"` when `dryRun` is true).

Report both results to the user verbatim.

**Delete legacy files (only when `migrate({action:"config"})`'s `result` started with
`"migrated"` and named a non-empty `legacy ingested: [...]` list):** parse the
space-separated paths out of the brackets in that string (e.g. `.claude/sdlc.json`,
`.claude/version.json`), then use AskUserQuestion:

> Delete the legacy config files that were just migrated? (`<comma-joined list>`)

Options:
- **yes** — delete the listed files (Bash `rm -f "<path>"` for each; git history keeps a
  backup)
- **no** — keep the legacy files alongside the migrated config

On **no** (top-level choice: configure from scratch): proceed directly to Step 3 without
migrating.

After migration (or after the delete-legacy prompt resolves), re-run Step 0's snapshot
(re-call `setup_prepare` and re-Read `.sdlc-v2/config.json` / `.sdlc-v2/local.json`) so Step 3's
"Current value" lines and Step 1's already-computed `state`/`summary` reflect the migrated
config.

---

### Step 3 — Dispatch Loop (Verbose Per-Section Configuration)

For each id in `selectedIds`, in canonical `internal/setupmeta.Sections()` order, look up
`section = sections.find(s => s.id === id)` and:

1. **Print the verbose header** (every line below sourced from `section.*` plus the `summary`
   computed in Step 1 — do NOT hardcode):

   ```
   --- Configuring: <section.label> ----------------------------------
   Purpose:        <section.purpose>

   Files modified: <section.filesModified joined with ", ">
   Consumed by:    <section.consumedBy joined with ", ">
   Config file:    <section.configFile> (path: <section.configPath || "—">)
   Current value:  <summary from Step 1, or "<none>">
   ```

2. **Print the per-option description block** (only when `section.fields.length > 0`):

   ```
   Options:
     <field.name>  ({field.type}, default: <field.default>)
                   <field.description>
     ...
   ```

3. **Run the dispatcher for the section's `delegatedTo` value**:

   | `delegatedTo` value | Dispatcher |
   |---|---|
   | (empty) | Generic field-loop (3.G below) — dispatch one AskUserQuestion per `section.fields[]` entry, optionally gated by `section.confirmDetected`. Applies to `version`, `ship`, `jira`, `review`, `received-review`, `plan-style`, `plan-tasks`. |
   | `'inline-commit-builder'` | Inline commit-pattern builder (3.commit below). |
   | `'inline-pr-builder'` | Inline PR-pattern builder (3.pr below). |
   | `'setup-dimensions'` | Run scan phase (3.S below), then read and follow `@setup-dimensions.md`, passing scan results as "Scan Input". Pass through `--add` and `--no-copilot` if present. |
   | `'setup-pr-template'` | Run scan phase (3.S), then read and follow `@setup-pr-template.md`, passing scan results. Pass through `--add` if present. |
   | `'setup-pr-labels'` | Read and follow `@setup-pr-labels.md` (it runs `gh label list` itself; no scan input from parent required). |
   | `'setup-guardrails'` | Read and follow `@setup-guardrails.md` (it runs its own scan internally). Pass through `--add` if present. |
   | `'setup-execution-guardrails'` | Read and follow `@setup-execution-guardrails.md`. Pass through `--add` if present. |
   | `'setup-openspec'` | Read and follow `@setup-openspec.md`. Pass through `--remove-openspec` as `--remove` if present. |
   | `'setup-plan-template'` | Read and follow `@setup-plan-template.md` (it checks for an existing template itself; no scan input from parent required). |

After the loop, write any pending project-config and local-config slices via the "Writing
config files" sub-section at the end of Step 3.

#### 3.G. Generic field loop (`delegatedTo` empty)

For sections with no `delegatedTo` (`version`, `ship`, `jira`, `review`, `received-review`,
`plan-style`, `plan-tasks`):

If `section.confirmDetected === true` (currently only `version`), dispatch a meta-prompt
FIRST using AskUserQuestion:

> Use detected settings, customize each field, or skip this section?

Options: `yes` (write detected values directly), `customize` (iterate `section.fields`),
`skip` (write nothing for this section).

- On **yes**: build the section value from the version detection gathered in Step 0 (e.g.
  `{ mode: 'file', versionFile, fileType, tagPrefix }`; if no version file was detected, use
  `{ mode: 'tag', tagPrefix }`). Do NOT write `preRelease` on the yes path. The pre-release
  compatibility check below does not apply on the yes path.
- On **customize**: continue to the field iteration below.
- On **skip**: stop processing this section; do not write anything.

For each entry `field` in `section.fields` (when iterating), dispatch one AskUserQuestion:

- **Question prompt:** `field.label`
- **Helper text:** `field.description` (verbatim from the manifest)
- **Choices:** `field.options` (or free-text input when `options` is empty)
- **Default:** `field.default`
- **Skip gate:** if `field.whenStepInActiveSteps` is set, skip this field entirely (do not
  ask, do not write a value) unless that step name is present in the `ship.steps` value the
  user already chose earlier in this same field loop.

Skip a field when an upstream answer makes it irrelevant: for `version`, skip `versionFile`
and `fileType` if `mode === 'tag'`; skip `changelogFile` if `changelog === false`; omit
`preRelease` from the written config when the user enters an empty string.

<!-- Implements R-version-prerelease-compat, G4. Fixes #338 (Gap B). -->
**Version pre-release compatibility check:**
After all `version` section fields are collected and BEFORE storing the section object:

1. If `mode === 'tag'` or `preRelease` is empty/omitted → skip this check.
2. Look up `<chosen fileType>` in this static table (ported verbatim from source's
   `PRE_RELEASE_COMPAT`, `scripts/skill/setup.js`):

   | fileType | level | message |
   |---|---|---|
   | `package.json` | compatible | — |
   | `cargo.toml` | compatible | — |
   | `plugin.json` | compatible | — |
   | `pyproject.toml` | partial | pyproject.toml uses PEP 440 pre-release format (e.g., `.rc1`), which differs from semver (`-rc.1`). The version bump, written post-merge by CI, may not parse cleanly with PEP 440 tooling. Confirm before writing. |
   | `pubspec.yaml` | incompatible | pubspec.yaml does not support semver pre-release labels. Setting preRelease will break the post-merge CI version bump. |
   | `version-file` | unknown | Plain text version files accept any string, but downstream tooling that reads this file may not. Confirm before writing. |

3. Branch on `level`:
   - `compatible` → store the section as-is; no prompt.
   - `partial` or `unknown` → print the message, then use AskUserQuestion (single-select):
     "Proceed with `preRelease: <value>` for `<fileType>`?" → options `yes` (store as-is),
     `no` (omit `preRelease` from the stored section).
   - `incompatible` → print the message, then use AskUserQuestion (single-select):
     "Pre-release labels are not supported for `<fileType>`. Clear `preRelease`, or proceed
     anyway?" → options `clear` (omit `preRelease`), `proceed` (store as-is, accepting risk).
4. This check runs once per version-section dispatch; do not re-ask if the same compat
   verdict was already resolved earlier in this same execution of Step 3.

**Answer mapping when assembling the section object:**
- `enum` fields → write the selected option string verbatim
- `multi-select` fields → write the array of selected options
- `boolean` fields → map `yes` → `true`, `no` → `false` (exception: `rebase` writes
  `auto`/`skip`/`prompt` verbatim — do NOT translate to yes/no)
- `string` fields → write the entered string; omit when empty (and the field is optional)
- `number` fields → coerce to an integer; validate against `field.min`/`field.max` when
  present; re-prompt on invalid input, citing the violated bound
- `list` fields → accept comma-separated input; split on `,` and trim each element to
  produce a string array (exception: `narrativeRules` — see below)
- `narrativeRules` (a `list` field on `plan-style`) → individual rules may themselves contain
  commas (e.g. "avoid idioms, jargon, and complex sentence structures"), so comma-splitting is
  unsafe. Prompt for free text with one rule per line; split on newline instead, trim each
  line, and drop empty lines.

You MUST issue exactly one AskUserQuestion per `section.fields[]` entry that survives the
gating above. Do not batch, reorder, or hand-enumerate fields — the manifest owns the list.

After the field loop, store the assembled section object keyed by id; the "Writing config
files" step will persist it.

#### 3.commit. Inline commit-pattern builder (`delegatedTo === 'inline-commit-builder'`)

The verbose header from Step 3 (purpose / files-modified / consumed-by / config-file /
current-value) has already been printed. Then run the conditional builder:

Use AskUserQuestion:

> Do you enforce commit message patterns in this project?

Options:
- **conventional** — Conventional commits: `type(scope): description`
- **ticket-prefix** — Ticket prefix: `PROJ-123: description`
- **custom** — Enter your own regex pattern
- **skip** — Don't configure commit patterns

On **conventional**: Use AskUserQuestion for sequential refinement:

1. "Require scope?" — yes / no → Determines `subjectPattern`:
   - yes: `^(feat|fix|refactor|chore|docs|test|ci)(\\(.*\\)): .+$`
   - no: `^(feat|fix|refactor|chore|docs|test|ci)(\\(.*\\))?: .+$`
2. "Allowed types?" — multi-select (feat, fix, refactor, chore, docs, test, ci; all selected
   by default) → Updates regex `(type1|type2|...)`
3. "Allowed scopes?" — free text comma-separated or skip → Adds scope constraint if provided:
   - If scopes provided: `^(types)(\\((scope1|scope2)\\)): .+$`
   - If skip: use pattern without scope constraint
4. "Require body for which types?" — multi-select (feat, fix, or skip) → Sets
   `requiresBody` array
5. "Required trailers?" — free text comma-separated (e.g., `Ticket`, `Reviewed-By`) or skip
   → Sets `trailers` array

Assemble the `commit` section object. Only include optional fields if the user provided
values; omit empty arrays.

On **ticket-prefix**: Use AskUserQuestion for sequential refinement:

1. "Ticket pattern?" — free text regex (default: `[A-Z]{2,10}-\\d+` for `PROJ-123`) → Sets
   `ticketPattern`
2. "Combine with conventional type?" — yes / no:
   - yes: `subjectPattern` becomes `^PROJ-\\d+ (feat|fix|...)(\\(.*\\))?: .+$`
   - no: `subjectPattern` becomes `^PROJ-\\d+: .+$`
3. If combined with types, ask the same type/scope/body/trailer refinement questions as
   **conventional**.

On **custom**: Use AskUserQuestion:

1. "Enter your regex pattern for commit subject:" → free text → `subjectPattern`
2. "Enter error message if pattern doesn't match:" → free text → `subjectPatternError`

On **skip**: Do not write a commit section.

Store the assembled `commit` config for use in the "Writing config files" step. `commit` is
not shared with any other section — write it wholesale.

#### 3.pr. Inline PR-pattern builder (`delegatedTo === 'inline-pr-builder'`)

Verbose header from Step 3 already printed. Then:

Use AskUserQuestion:

> Do you enforce PR title patterns?

Options:
- **same-as-commit** — Use the same pattern as commit (only when 3.commit produced a config)
- **conventional** — Conventional format
- **ticket-prefix** — Ticket prefix format
- **custom** — Enter your own regex
- **skip** — Don't configure PR title patterns

On **same-as-commit** (if available): Copy the commit config fields to PR config with
renamed fields: `subjectPattern` → `titlePattern`, `subjectPatternError` →
`titlePatternError`. Keep `allowedTypes`, `allowedScopes`, `requiresBody`, `trailers`
as-is.

On **conventional**: Use sequential AskUserQuestion:

1. "Allowed types?" — multi-select (feat, fix, refactor, chore, docs, test, ci; all selected
   by default)
2. "Require scope?" — yes / no
3. "Allowed scopes?" — free text comma-separated or skip
4. "Required trailers?" — free text comma-separated or skip

On **ticket-prefix**: Ask the same questions as commit (ticket pattern, combine with types,
etc.).

On **custom**: Ask:

1. "Enter your regex pattern for PR title:" → free text → `titlePattern`
2. "Enter error message if pattern doesn't match:" → free text → `titlePatternError`

On **skip**: Do not write a `pr` section.

Also collect `defaultBranch` and `expectedAccount` per `prFields` (`internal/setupmeta`):
default `defaultBranch` to the `defaultBranch` value returned by `setup_prepare`; default
`expectedAccount` to `remoteOwner` from the same call. Offer both as editable defaults via
AskUserQuestion rather than silently accepting them.

Store the assembled `pr` config for use in the "Writing config files" step. **`pr` is shared
with `pr-labels`** — see "Writing config files" below for the required read-preserve-write
sequencing; never write the object assembled here directly with `setup_write_sections`
without first merging in any existing `pr.labels`.

#### 3.S. Scan phase (delegated content sections only)

Before invoking `setup-dimensions` or `setup-pr-template`, run the project signal scan:

> **Shell safety:** Use the **Glob** tool for all file/directory existence checks. Do NOT use
> Bash `ls` with glob patterns — zsh (macOS default) errors on unmatched globs. Use Bash only
> for `git` commands, `gh` CLI, and `which`.

- **Dependency manifests:** Glob for `package.json`, `requirements.txt`, `Pipfile`,
  `pyproject.toml`, `Cargo.toml`, `go.mod`, `pom.xml`, `build.gradle`. Read each found file.
- **Framework config:** Glob for `**/jest.config.*`, `**/vitest.config.*`, `**/.eslintrc*`,
  `**/tsconfig.json`, `**/openapi.yaml`, `**/openapi.json`, `**/.prettierrc*`.
- **Directory structure:** Glob for `src/`, `lib/`, `controllers/`, `services/`,
  `middleware/`, `models/`, `routes/`, `api/`, `pkg/`, `cmd/`, `internal/` and patterns from
  `@scan-patterns.md`.
- **CI/CD config:** Glob for `.github/workflows/*.yml`, `Jenkinsfile`, `.circleci/config.yml`,
  `.gitlab-ci.yml`.
- **Database presence:** Glob for `prisma/`, `migrations/`, `alembic.ini`, `db/migrate/`,
  `**/sequelize*`, `**/typeorm*`, `**/sqlalchemy*`.
- **Test structure:** Glob for `test/`, `tests/`, `spec/`, `__tests__/`, `cypress/`,
  `**/playwright.config.*`.
- **Existing review dimensions:** Glob for `.sdlc-v2/review-dimensions/*` (count and names;
  reuse the Step 0 snapshot when this is the first delegated section in the loop).
- **Existing guardrails:** Read `.sdlc-v2/config.json` → `plan.guardrails` array if present.
- **GitHub hosting detection:** Bash for `git remote -v` and `gh repo view` (safe). Glob for
  `.github/`.
- **CLAUDE.md / AGENTS.md:** Read `CLAUDE.md`, `AGENTS.md`, `.claude/CLAUDE.md` if present.
- **PR template:** Glob for `.github/PULL_REQUEST_TEMPLATE.md`,
  `.github/pull_request_template.md`.
- **Recent PRs:** Bash for `gh pr list --limit 5 --json title,body` (safe).
- **Existing PR template:** Glob for `.sdlc-v2/pr-template.md` (reuse Step 0 snapshot).
- **JIRA evidence:** Bash for `git log --oneline -20` and `git rev-parse --abbrev-ref HEAD`
  (safe).

Collect all signals into a "Scan Input" object to pass to the sub-flow. Run the scan once
per setup invocation; cache the result for any subsequent delegated section in the same
`selectedIds` list.

#### Legacy section reference

The historical step labels map onto the dispatcher above for anyone updating tests or docs:

| Legacy step | Manifest section id | Dispatcher branch |
|---|---|---|
| 3a | `version` | 3.G with `confirmDetected: true` |
| 3b | `ship` | 3.G |
| 3c | `jira` | 3.G |
| 3d | `review` | 3.G |
| 3e | `commit` | 3.commit |
| 3f | `pr` | 3.pr |

(Source's `3g`/`3h` rows — `workspace`/`hooks` — are dropped per Q1; there is no manifest id
for them in this port.)

#### Diff preview (Gap C)

Before writing, render an end-of-run diff preview comparing the Step 0 snapshot
(`projectConfig`/`localConfig`) against the values assembled in Step 3. There is no Go
equivalent of `computeConfigDiff` — build the table directly:

For each config key touched during Step 3 (i.e. each key that will be passed to
`setup_write_sections` in "Writing config files" below), compare the pre-Step-3 snapshot
value at that path to the newly assembled value and list only the paths whose value
actually changed:

```text
| path                      | before        | after         |
|---------------------------|---------------|---------------|
| pr.expectedAccount        | (unset)       | rnagrodzki    |
| version.tagPrefix         | v             | release/      |
```

When no path changed, skip the preview and print `No changes — nothing to write.`; bypass
the write step and proceed directly to Step 3b (a no-op confirmation in that case).

Otherwise, ask the user to confirm the diff via AskUserQuestion. On rejection, print
`Write cancelled — no changes made.` and skip the write step.

#### Writing config files

After collecting all answers AND confirming the diff preview above:

1. **Assemble the write map.** For each section actually configured in Step 3 (not skipped),
   compute its `setup_write_sections` key as the segment of `section.configPath` before its
   first `.` (see Port Notes): `version`→`version`, `ship`→`ship`, `jira`→`jira`,
   `review`→`review`, `received-review`→`receivedReview`, `commit`→`commit`, `pr`→`pr`,
   `pr-labels`→`pr` (nested `labels`), `plan-style`→`planStyle`, `plan-tasks`→`plan` (nested
   `tasks`), `plan-guardrails`→`plan` (nested `guardrails`),
   `execution-guardrails`→`execute` (nested `guardrails`).

   Note: `pr-labels`, `plan-guardrails`, and `execution-guardrails` are configured by their
   own companion sub-flows (`setup-pr-labels.md`, `setup-guardrails.md`,
   `setup-execution-guardrails.md`), which each call `setup_write_sections` themselves
   using their own read-merge-write sequencing — do not re-write those keys here.

2. **`pr` merge-preserve.** If `pr` was configured in 3.pr this run, immediately before
   writing, Read the current `.sdlc-v2/config.json` and check for an existing `pr.labels` key
   (it may have been written by `setup-pr-labels.md` in an earlier or the same run). If
   present, include it unchanged in the object being written:

   ```
   setup_write_sections({
     sectionsJson: JSON.stringify({
       pr: { ...assembledPrFromStep3, labels: <existing pr.labels if present> }
     })
   }) → { ok, written, errors }
   ```

3. **`plan` merge-preserve.** If `plan-tasks` was configured in the generic field loop (3.G)
   this run, immediately before writing, Read the current `.sdlc-v2/config.json` and check for
   an existing `plan.guardrails` key (it may have been written earlier in this same run by
   `setup-guardrails.md`, which writes immediately rather than deferring to this step, or by a
   prior run). If present, include it unchanged in the object being written:

   ```
   setup_write_sections({
     sectionsJson: JSON.stringify({
       plan: { tasks: <assembledPlanTasksFromStep3>, guardrails: <existing plan.guardrails if present> }
     })
   }) → { ok, written, errors }
   ```

   Omit the `guardrails` key entirely when no existing value is present — do not write
   `guardrails: []`.

4. **Everything else** writes directly, one key per assembled section, in a single batched
   call where possible:

   ```
   setup_write_sections({
     sectionsJson: JSON.stringify({
       version: { ... }, ship: { ... }, jira: { ... }, review: { ... },
       receivedReview: { ... }, commit: { ... }, planStyle: { ... }
     })
   }) → { ok, written, errors }
   ```

   Omit any key whose section was skipped or not selected. Display `written[]` and any
   `errors[]` from the response.

---

### Step 3b — Validate Written Config

Re-run Step 0's snapshot (re-call `setup_prepare`, re-Read `.sdlc-v2/config.json` and
`.sdlc-v2/local.json`) and recompute `state` for every id that was just written.

Confirm every id written in "Writing config files" now shows `state === 'set'`. If any
written id still shows `not-set` (write silently no-opped or the value resolved as empty),
warn the user and offer to retry that section's write. Do not proceed to Step 4 while a
just-written section still reads back as `not-set`.

---

### Step 4 — Summary

Show what was created or updated:

```
Setup complete
---------------------------------------------------
Created/updated:
  .sdlc-v2/config.json      — project config (version, jira, ...)
  .sdlc-v2/local.json       — local config (review, ship, ...)

Content:
  Review dimensions       — [installed via dimensions sub-flow | skipped]
  PR template             — [installed via PR template sub-flow | skipped]
  Plan guardrails         — [N configured via guardrails sub-flow | skipped]

Migrated:
  .claude/version.json    — merged into .sdlc-v2/config.json [deleted | kept]
  ...
```

Only show sections that were actually created, updated, or migrated. Omit sections that were
skipped or unchanged.

---

## Idempotency

This skill is safe to re-run. Already-configured sections show `[set]` in Step 1 and are
skipped by the `not-set` menu token unless `--force` is passed. `setup_write_sections` /
`config.WriteSection` replace a section wholesale — see "Writing config files" for the two
cases (`pr`, `plan`) where this port must explicitly re-read and merge before writing, since
Go has no read-merge-write primitive equivalent to source's
`writeProjectConfig`/`writeLocalConfig`.

---

## DO NOT

- Run full-suite or wide-subset `promptfoo eval` automatically — a single targeted test
  scoped to the change is allowed; tight-loop retries are not.
- Delete legacy files without explicit user confirmation via AskUserQuestion.
- Invoke a companion sub-flow via the Agent tool — use the Skill/Read-and-follow pattern
  exclusively (`@setup-dimensions.md`, `@setup-pr-template.md`, etc. are markdown files this
  skill reads and follows inline, not subagents).
- Modify Jira templates directly — delegate to `/jira` via the Skill tool.
- Write config files using the Write or Edit tools directly — always go through
  `setup_write_sections` (or a companion sub-flow's own call to it).
- Skip AskUserQuestion for any user interaction — do not print questions and wait for
  freeform input, except at the two literal-chat-output points explicitly marked as such in
  Step 1 (Phases 1–4).
- Assume `mode` for the `version` section — it is a required field, always ask or detect.
- Write the `pr` config key without first checking for and preserving an existing
  `pr.labels` sibling (see "Writing config files").
- Write the `plan` config key without first checking for and preserving whichever sibling
  (`plan.guardrails` or `plan.tasks`) wasn't just configured (see "Writing config files").

---

## Gotchas

**`setup_prepare` must run from the project root.** It resolves the worktree main root
internally; if invoked from an unexpected working directory inside a detached checkout,
detection may return unexpected results.

**The `version` section requires `mode` as a required field.** When a version file was
detected in Step 0, default to `mode: "file"`. When none was found, default to
`mode: "tag"`. Always include `mode` in the written config.

**Ship config is developer-local.** Ship preferences live in `.sdlc-v2/local.json` (gitignored),
not in `.sdlc-v2/config.json`. Each developer has their own ship preferences.

**`setup_write_sections` is wholesale, not merge, per key.** Unlike source's
`writeProjectConfig`/`writeLocalConfig`, there is no automatic read-merge-write across an
entire config file — each call replaces exactly the top-level keys it names. The `pr` /
`pr.labels` and `plan.tasks` / `plan.guardrails` interactions (see "Writing config files")
are the places in this file where that distinction has an observable correctness
consequence; the `setup-pr-labels.md`, `setup-guardrails.md`, and
`setup-execution-guardrails.md` companion sub-flows each handle their own equivalent
read-preserve-write internally.

**Legacy review config has two possible locations.** `.sdlc-v2/review.json` and
`.claude/review.json` are both legacy paths; `internal/configmigrate` prefers
`.sdlc-v2/review.json` when both exist.

**`state`/`summary` are computed by this skill, not returned by any tool.** If a future Go
tool version adds these fields to `setup_prepare`'s output, prefer the tool's values and
drop the LLM-native computation in Step 1 — this port note (Gap A) exists specifically
because that field does not exist yet.

---

## Learning Capture

After completing setup or encountering unexpected behavior, call:

```
learnings_log({action: "append", entry: "## YYYY-MM-DD — setup: <brief summary>\n<what happened, what was learned>"})
```

Record entries for: projects with unusual version file locations, migration edge cases,
legacy file conflicts, or user preferences that differ from defaults.

---

## See Also

- [`/ship`](../ship/SKILL.md) — end-to-end feature shipping pipeline
- [`/review`](../review/SKILL.md) — multi-dimension code review
- [`/jira`](../jira/SKILL.md) — Jira integration
- `setup-dimensions.md`, `setup-pr-template.md`, `setup-pr-labels.md`, `setup-guardrails.md`,
  `setup-execution-guardrails.md`, `setup-openspec.md`, `setup-plan-template.md` — companion
  sub-flows dispatched from Step 3
