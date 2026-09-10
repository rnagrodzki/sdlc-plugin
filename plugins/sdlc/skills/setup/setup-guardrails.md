# Guardrails Sub-Flow

Sub-flow of `/setup --guardrails`. Scans the project and generates
guardrail proposals for the `plan` section, then lets the user review and
select. Writes guardrails to `.sdlc-v2/config.json` via `setup_write_sections`.

> **Port Notes** (Task 44 KD9 rewrite): `skill/guardrails.js` (signal
> detection + template-catalog matching) has no Go tool equivalent — no
> dedicated Go tool exists for project-wide guardrail scanning. Step 0 below
> replaces it with an LLM-native scan against the Guardrail Catalog (inlined
> below), mirroring the pattern setup's own Step 3.S "Scan phase" and
> setup-dimensions.md already use for dimension proposals. `validate-guardrails.js`
> → `validate({ action: "guardrails", section: "plan" })`. Config writes go
> through `setup_write_sections` instead of an inline `node -e` call to
> `lib/config.js::writeSection`. The `plan` config section now has a sibling
> `tasks` sub-key (the `plan-tasks` setup section) alongside `guardrails`.
> `setup_write_sections` replaces the `plan` top-level key wholesale, so
> Step 0 below reads and Step 3 re-writes the existing `plan.tasks` value
> unchanged — same read-preserve-write hazard as `pr`/`pr.labels`, see
> setup-pr-labels.md.

## Arguments

| Flag | Description | Default |
|------|-------------|---------|
| `--add` | Expansion mode: propose only guardrails not already configured | off |

## Guardrail Catalog

Evidence-to-guardrail mapping (source of truth: this table). Only propose a
guardrail when its evidence condition is actually observed.

**Conditional (evidence-gated):**

| ID | Category | Severity | Evidence condition | Plan description |
|---|---|---|---|---|
| `no-direct-db-access` | architecture | error | Database detected (see "Database detection" below) AND a `repositories/` or `src/repositories/` directory exists | Plans must route all database operations through the repository layer. No direct SQL or ORM calls in controllers/handlers. |
| `api-backward-compatibility` | architecture | error | API detected (see "API detection" below) | Plans must version or deprecate changed APIs. Breaking changes must be documented in Key Decisions. |
| `test-coverage-required` | testing | error | A `test/`, `tests/`, or `__tests__/` directory exists, OR `jest`/`vitest`/`mocha` in package.json deps, OR any `*_test.go` file exists | Every task that creates or modifies source code must include corresponding test cases. |
| `no-real-fs-git-in-tests` | testing | error | Any `*_test.go` file exists | Tests must not perform real filesystem operations (os.WriteFile, os.Create, os.MkdirTemp, t.TempDir) or execute real git/gh commands (exec.Command, execx.Run). Use dependency injection with mock/fake implementations instead. |
| `database-migration-review` | architecture | warning | Database detected | Tasks modifying database schema must be flagged as High risk. |
| `no-ci-bypass` | security | error | `.github/workflows/*.yml`, `Jenkinsfile`, or `.gitlab-ci.yml` exists | Plans must not include steps that skip or disable CI checks. |
| `monorepo-boundary-respect` | architecture | warning | `lerna.json`, `pnpm-workspace.yaml`, or `nx.json` exists | Tasks must not create cross-package dependencies without explicit justification. |
| `spec-compliance` | architecture | warning | `openspec/config.yaml` exists | Changes must reference an OpenSpec change for traceability. |
| `island-only-interactivity` (svelte) | framework | warning | `svelte` in package.json deps | Tasks must use Svelte components as interactive islands only — default to static markup and reach for `<script>` blocks only where interactivity is required. |
| `island-only-interactivity` (astro) | framework | warning | `astro` in package.json deps | Tasks must keep Astro components static by default — opt into client hydration (`client:load`, `client:idle`, `client:visible`) only for components that require interactivity. |
| `prisma-migration-required` | framework | error | `prisma/` directory exists | Tasks that modify the Prisma schema must include a corresponding `prisma migrate` step. Schema changes without migrations are a blocking issue. |

**Always-on (propose regardless of evidence):**

| ID | Category | Severity | Plan description |
|---|---|---|---|
| `no-scope-creep` | scope | warning | Tasks must only address stated requirements; no gold-plating. |
| `single-responsibility-tasks` | scope | warning | Each task must have exactly one clear deliverable. |
| `yagni` | scope | warning | Tasks must not add functionality beyond stated requirements — no speculative abstractions, premature generalization, or unused parameters. |
| `dry` | quality | warning | Tasks must not duplicate logic that exists elsewhere — reuse existing functions or extract shared utilities. |
| `kiss` | quality | warning | Tasks must prefer the simplest design that satisfies requirements — avoid unnecessary abstractions and over-engineering. |
| `prefer-mcp-over-cli` | process | warning | Plans must route SDLC-process steps (setup, version, ship, review, commit, PR, jira) through this project's MCP tools rather than raw CLI invocations. Where an MCP tool covers the step, do not add a task that shells out for the same result. Batch what the MCP surface returns in one call rather than planning multiple round trips between harness and model. |
| `mcp-error-has-suggestion` | mcp | error | Every new MCP error return path where recovery is possible must populate the Suggestion field on DomainError/InfraError/DataError. Raw error messages without structured recovery hints force the LLM to parse free-text. |
| `mcp-output-drives-behavior` | mcp | warning | Tool output structs (*Out) should include a Next string field (json:"next") with an exact per-outcome hint guiding the LLM's next action. Outputs that return data without behavioral guidance leave the LLM to guess the next step. |
| `mcp-parameter-documented` | mcp | error | Every exported field on an *In struct must have a jsonschema_description tag. Fields representing closed sets (validated by switch/const) must additionally have a jsonschema enum tag. Undocumented parameters create ambiguity for the LLM. |

**Planning-discipline (this sub-flow only — plan-target; never proposed by setup-execution-guardrails.md):**

| ID | Category | Severity | Plan description |
|---|---|---|---|
| `state-premise` | planning-discipline | error | Plan must state the underlying problem in one sentence — in Goal, Architecture, or Key Decisions. Reject plans that transcribe requirements without naming the root problem. |
| `smallest-viable-cut` | planning-discipline | warning | Plan must identify the smallest version of the work that still delivers value. If the plan covers more than the minimum, each extra task must be justified in Key Decisions. |
| `failure-audience` | planning-discipline | warning | Plan must name who or what fails if this implementation ships incorrectly (user, system, team, downstream service). Surfaces the blast radius before decomposition. |
| `explicit-non-goals` | planning-discipline | warning | Plan must list what is intentionally NOT being built. Surface adjacent improvements that were considered and excluded so reviewers and executors do not silently re-expand scope. |
| `ambiguity-stops-and-asks` | planning-discipline | error | When two or more codebase patterns are viable for the same requirement with materially different downstream implications, the plan must EITHER record the choice in Key Decisions with rationale, OR the skill must AskUserQuestion before committing. Silent selection between viable patterns is a critique failure. |

**Detection helpers:**

- **Database detection**: `prisma/` dir → prisma; `alembic/` dir → alembic; `knexfile.{js,ts,cjs}` → knex; `typeorm` in deps → typeorm; `mongoose`/`mongodb`/`redis`/`ioredis`/`@aws-sdk/*dynamodb*` in deps → nosql driver; a `migrations/` directory also implies a database.
- **API detection**: `openapi.yaml`/`openapi.json` → openapi; any `*.graphql`/`*.gql` file or `schema.graphql` → graphql; a `routes/` or `src/routes/` directory → rest.
- Use Glob for directory/file existence checks (see also `./scan-patterns.md` for generic structural globs) and Read for `package.json`/`go.mod`/`Cargo.toml`/`pyproject.toml`/`requirements.txt` dependency checks.

## Workflow

### Step 0 — Prepare

1. Read `.sdlc-v2/config.json`. Extract the existing `plan.guardrails` array (empty if absent) as `existing`. Also extract the existing `plan.tasks` object (absent if not configured) as `existingTasks` — it must be written back unchanged in Step 3 (see "plan merge-preserve" note there).
2. If not in `--add` mode and `existing` is non-empty: use AskUserQuestion: "`{existing.length}` guardrails already configured. Replace all, or use --add to expand?" Options: replace / cancel. On cancel, stop.
3. Run the scan (Glob + Read, per Detection Helpers above) and build the evidence set.
4. Optionally extract candidate rules from `CLAUDE.md`/`AGENTS.md`: lines containing "must", "never", "always", "require(s/d)", "forbidden", or "prohibited" (first 20 matches, deduplicated). Use these only as extra evidence for Step 1, not as guardrails to propose verbatim.

### Step 1 (REVIEW) — Build and Refine Proposals

Using the Guardrail Catalog and the scan results:

1. Include every conditional guardrail whose evidence condition is met.
2. Include every always-on guardrail.
3. Include every planning-discipline guardrail (this sub-flow always targets `plan`).
4. In `--add` mode: exclude any `id` already present in `existing`.
5. Consider whether any `CLAUDE.md`/`AGENTS.md` candidate rules suggest an additional project-specific guardrail beyond the catalog.
6. Drop proposals that don't make sense despite matching a signal (e.g. a stale `migrations/` directory with no active database).
7. Cap at 3-8 proposals — if more match, keep the highest-severity and most-specific ones.

This is lightweight filtering against a fixed catalog, not open-ended generation.

### Step 2 (PRESENT) — Interactive Selection

Present refined proposals as a numbered list with evidence. Each proposal
must display its `[category]` tag (implements R-#336-category). Format:

```
Proposed guardrails:
  1. [planning-discipline]  state-premise — Plan must state the underlying problem in one sentence
  2. [framework]            island-only-interactivity — Use island pattern for interactive components only
  3. [scope]                no-scope-creep — Implementation must stay within the task's stated scope
  ...
```

**Stage A — Standard selection**

Use AskUserQuestion:

> Install which guardrails?

Options:

- **all** — install all proposed guardrails
- **select** — comma-separated numbers to install a subset
- **cancel** — exit without changes (skips Stage B)

**Stage B — Custom guardrails (always-on, unless Stage A was cancelled)**

After Stage A completes (whether any standard guardrails were selected or
not), always run this prompt. Use AskUserQuestion:

> Add custom project-specific guardrails?

Options: **yes** / **no**

On **yes**: enter per-guardrail id/description/severity loop:
- Collect id (validate kebab-case pattern `^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
- Collect description
- Collect severity (default: error)
- After each entry ask: "Add another custom guardrail?" yes/no — loop until no

Allow multiple custom entries. Custom guardrails are added alongside the
standard selections from Stage A.

### Step 3 (WRITE) — Write Config

**`plan` merge-preserve.** `setup_write_sections` replaces the `plan` top-level
key wholesale. If `existingTasks` (from Step 0) is non-empty, it MUST be
included unchanged in this call, or a `plan-tasks` configuration written
earlier (this run or a prior run) is silently wiped:

```
setup_write_sections({
  sectionsJson: JSON.stringify({
    plan: { guardrails: <FULL_GUARDRAILS_ARRAY>, tasks: <existingTasks, if present> }
  })
}) → { ok, written, errors }
```

Omit the `tasks` key entirely when `existingTasks` is absent — do not write
`tasks: {}`. `<FULL_GUARDRAILS_ARRAY>` is the selected guardrails from Step 2.
In `--add` mode: prepend `existing` (from Step 0) to the array before
writing — the write is wholesale replacement, not a merge.

### Step 4 (VALIDATE)

```
validate({ action: "guardrails", section: "plan" }) → { findings }
```

If `findings` is empty, report success with the count written. If non-empty,
show the findings and offer to fix them.

## Do Not

- Run full-suite or wide-subset `promptfoo eval` automatically — a single targeted test scoped to the change is allowed; tight-loop retries are not.
- Write config files using Write or Edit tools directly — always use `setup_write_sections`.
- Skip AskUserQuestion for user interaction.
- Scan the entire codebase — use the Guardrail Catalog's evidence conditions, not an unbounded scan.

## Gotchas

- **The Guardrail Catalog above is the source of truth for scanning.** Do not invent guardrails outside it except through the Stage B custom-guardrail path.
- **Config write is wholesale, not merge.** `setup_write_sections` replaces the `plan` section entirely. In `--add` mode, the skill must read existing guardrails (Step 0) and prepend them to the selection before writing. It must also re-include `existingTasks` (Step 0) unchanged, since `plan-tasks` shares the same `plan` top-level key.
- **Stage B runs after any non-cancel Stage A outcome.** If the user selects all/select in Stage A with no custom entries expected, Stage B still runs — it is always-on. Only a Stage A cancel skips Stage B.
- **Custom guardrails need ID validation.** The kebab-case pattern `^[a-z][a-z0-9]*(-[a-z0-9]+)*$` must be enforced in Stage B before writing.

## See Also

- [`/plan`](../plan/SKILL.md) — consumes guardrails during critique phases
- [`/setup --guardrails`](../setup/SKILL.md) — parent skill that delegates guardrail setup to this sub-flow
- [`/setup --dimensions`](../setup/SKILL.md) — analogous pattern for review dimensions
- [`setup-execution-guardrails.md`](./setup-execution-guardrails.md) — execution guardrails analog (same catalog, `execute` target)
