# Execution Guardrails Sub-Flow

Sub-flow of `/setup --execution-guardrails`. Scans the project and
generates execution-focused guardrail proposals for the `execute` section,
then lets the user review and select. Writes guardrails to
`.sdlc-v2/config.json` via `setup_write_sections`.

> **Port Notes** (Task 44 KD9 rewrite): same as `setup-guardrails.md` — no Go
> tool equivalent exists for `skill/guardrails.js`'s scanning, so Step 0 below
> is an LLM-native scan against the Guardrail Catalog in `setup-guardrails.md`
> (shared table, not duplicated here — this file supplies the execute-target
> description column only). `validate-guardrails.js --section execute` →
> `validate({ action: "guardrails", section: "execute" })`. Config writes go
> through `setup_write_sections` (`{"execute": {"guardrails": [...]}}`),
> wholesale — the `execute` config section's schema has a single `guardrails`
> array property, same as `plan`.

## Arguments

| Flag | Description | Default |
|------|-------------|---------|
| `--add` | Expansion mode: propose only guardrails not already configured | off |

## Guardrail Catalog (execute target)

Use the evidence conditions and detection helpers from
[`setup-guardrails.md`](./setup-guardrails.md)'s Guardrail Catalog. The
**Planning-discipline** rows there are plan-target only — never propose them
here. For all Conditional and Always-on rows, use these execute-oriented
descriptions instead of the plan descriptions:

| ID | Category | Severity | Execute description |
|---|---|---|---|
| `no-direct-db-access` | architecture | error | Code changes must not introduce direct database queries outside the repository layer. |
| `api-backward-compatibility` | architecture | error | API changes must maintain backward compatibility — no breaking changes to existing endpoints or contracts without versioning. |
| `test-coverage-required` | testing | error | Code changes must include corresponding test coverage — verify tests exist and pass after each wave. |
| `no-real-fs-git-in-tests` | testing | error | Implementation must not use real filesystem operations (os.WriteFile, os.Create, os.MkdirTemp, t.TempDir) or execute real git/gh commands (exec.Command, execx.Run) in tests. Use dependency injection with mock/fake implementations. |
| `database-migration-review` | architecture | warning | Database migration files must be reviewed — schema changes are flagged for manual verification. |
| `no-ci-bypass` | security | error | Implemented code must not disable, skip, or weaken CI checks, linters, or pre-commit hooks. |
| `monorepo-boundary-respect` | architecture | warning | Code changes must respect monorepo package boundaries — no cross-package imports outside declared dependencies. |
| `spec-compliance` | architecture | warning | Implementation must comply with OpenSpec delta spec requirements when available. |
| `island-only-interactivity` (svelte) | framework | warning | Use Svelte components as interactive islands only; default to static markup. Add `<script>` interactivity only where it is required, not by default. |
| `island-only-interactivity` (astro) | framework | warning | Astro components must be static by default. Use `client:*` directives only for components that genuinely require client-side interactivity; avoid unnecessary hydration. |
| `prisma-migration-required` | framework | error | Prisma schema changes must be accompanied by a migration file (`prisma migrate dev` or `prisma migrate deploy`). Do not modify schema.prisma without generating a migration. |
| `no-scope-creep` | scope | warning | Implementation must stay within the task's stated scope — no additional features, refactoring, or cleanup beyond what was specified. |
| `single-responsibility-tasks` | scope | warning | Each implemented change must address exactly one concern — no bundled fixes or unrelated modifications. |
| `yagni` | scope | warning | Do not add functionality until it is actually needed. No speculative abstractions, premature generalization, or unused parameters. |
| `dry` | quality | warning | Do not duplicate logic. If the same behavior exists elsewhere, reuse it or extract a shared function. |
| `kiss` | quality | warning | Prefer the simplest implementation that satisfies the requirements. Avoid unnecessary abstractions and over-engineered solutions. |
| `prefer-mcp-over-cli` | process | warning | Do not shell out via CLI for a step this project's MCP tools already cover (setup, version, ship, review, commit, PR, jira). Call the MCP tool instead, and batch multiple checks into as few calls as possible — every extra CLI round trip between harness and model slows execution and fragments feedback. |
| `mcp-schema-tags-required` | mcp | error | New or modified *In structs must have jsonschema_description tags on all exported fields. Verify by checking that mcp.WithInputSchema[StructName]() produces non-empty description for every property in the generated schema. |
| `skill-tool-param-sync` | mcp | warning | When a SKILL.md dispatches an MCP tool, every parameter name in the dispatch args must match a json tag on the tool's *In struct. Mismatched parameter names silently drop values and produce incorrect tool behavior. |

## Workflow

### Step 0 — Prepare

1. Read `.sdlc-v2/config.json`. Extract the existing `execute.guardrails` array (empty if absent) as `existing`.
2. If not in `--add` mode and `existing` is non-empty: use AskUserQuestion: "`{existing.length}` execution guardrails already configured. Replace all, or use --add to expand?" Options: replace / cancel. On cancel, stop.
3. Run the scan per `setup-guardrails.md`'s Detection Helpers.

### Step 1 (REVIEW) — Build and Refine Proposals

Using the Guardrail Catalog (Conditional + Always-on rows only — never
Planning-discipline) and the scan results:

1. Include every conditional guardrail whose evidence condition is met, using the execute description above.
2. Include every always-on guardrail, using the execute description above.
3. In `--add` mode: exclude any `id` already present in `existing`.
4. Drop proposals that don't make sense despite matching a signal.
5. Cap at 3-8 proposals.

This is lightweight filtering for runtime code constraints, not open-ended generation.

### Step 2 (PRESENT) — Interactive Selection

Present refined proposals as a numbered list with evidence. Use AskUserQuestion:

> Install which execution guardrails?

Options:

- **all** — install all proposed guardrails
- **select** — comma-separated numbers to install a subset
- **custom** — prompt user for id, description, severity to add alongside selections
- **cancel** — exit without changes

On **custom**: collect id (validate kebab-case pattern
`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`), description, severity (default: error).
Allow multiple custom entries.

### Step 3 (WRITE) — Write Config

```
setup_write_sections({
  sectionsJson: JSON.stringify({
    execute: { guardrails: <FULL_GUARDRAILS_ARRAY> }
  })
}) → { ok, written, errors }
```

`<FULL_GUARDRAILS_ARRAY>` is the selected guardrails from Step 2. In `--add`
mode: prepend `existing` (from Step 0) to the array before writing — the
write is wholesale replacement, not a merge.

### Step 4 (VALIDATE)

```
validate({ action: "guardrails", section: "execute" }) → { findings }
```

If `findings` is empty, report success with the count written. If non-empty,
show the findings and offer to fix them.

## Do Not

- Run full-suite or wide-subset `promptfoo eval` automatically — a single targeted test scoped to the change is allowed; tight-loop retries are not.
- Write config files using Write or Edit tools directly — always use `setup_write_sections`.
- Skip AskUserQuestion for user interaction.
- Scan the entire codebase — use the Guardrail Catalog's evidence conditions, not an unbounded scan.
- Propose any Planning-discipline guardrail — those are `plan`-target only (see `setup-guardrails.md`).

## Gotchas

- **The Guardrail Catalog (`setup-guardrails.md`) is the source of truth for scanning.** Do not invent guardrails outside it except through the custom-guardrail path.
- **Config write is wholesale, not merge.** `setup_write_sections` replaces the `execute` section entirely. In `--add` mode, the skill must read existing guardrails (Step 0) and prepend them to the selection before writing.
- **Custom guardrails need ID validation.** The kebab-case pattern `^[a-z][a-z0-9]*(-[a-z0-9]+)*$` must be enforced before writing.

## See Also

- [`/execute`](../execute/SKILL.md) — consumes execution guardrails during plan execution
- [`/setup --execution-guardrails`](../setup/SKILL.md) — parent skill that delegates execution guardrail setup to this sub-flow
- [`setup-guardrails.md`](./setup-guardrails.md) — plan guardrails analog; holds the shared Guardrail Catalog
