# Execution Guardrails Sub-Flow

Sub-flow of `/setup --execution-guardrails`. Scans the project and
generates execution-focused guardrail proposals for the `execute` section,
then lets the user review and select. Writes guardrails to
`.sdlc-v2/config.toml` via `setup_write_sections`.

> **Port Notes** (Task 44 KD9 rewrite): same as `setup-guardrails.md` — no Go
> tool equivalent exists for `skill/guardrails.js`'s scanning, so Step 0 below
> is an LLM-native scan against the Guardrail Catalog in `setup-guardrails.md`
> (shared table, not duplicated here — this file supplies the execute-target
> description column only). `validate-guardrails.js --section execute` →
> `validate({ action: "guardrails", section: "execute" })`. Config writes go
> through `setup_write_sections` using per-id `execute.guardrails.<id>` leaves
> in both modes — never the `execute` top-level key or a whole-table
> `execute.guardrails` write. No tool that this sub-flow calls returns the
> current `execute.guardrails` table. `prepare_orchestrator` in harden mode
> (field `executeGuardrails`) returns it, but this sub-flow does not call
> it. So neither mode can detect which ids are already configured — see
> Gotchas.

## Arguments

| Flag | Description | Default |
|------|-------------|---------|
| `--add` | Same behavior as the default mode in this sub-flow: the proposals are not deduplicated against configured ids, and a reselected id overwrites its description and severity | off |

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

1. Run the scan per `setup-guardrails.md`'s Detection Helpers.

No `.sdlc-v2/config.toml` read happens here. No tool that this sub-flow
calls returns the current `execute.guardrails` table, so this
sub-flow cannot tell whether execution guardrails are already configured
or which ids exist. `--add` and the default mode behave the same: the
proposal list is not deduplicated against configured ids (see Gotchas).

### Step 1 (REVIEW) — Build and Refine Proposals

Using the Guardrail Catalog (Conditional + Always-on rows only — never
Planning-discipline) and the scan results:

1. Include every conditional guardrail whose evidence condition is met, using the execute description above.
2. Include every always-on guardrail, using the execute description above.
3. `--add` mode is the same as the default mode: there is no list of
   configured ids to exclude (see Step 0) — propose from the full catalog
   as usual.
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

In both modes, warn before this prompt: "This project's current execution
guardrails can't be listed automatically — if any proposal below is already
configured, selecting it will overwrite its description/severity with the
catalog's values."

### Step 3 (WRITE) — Write Config

Both modes write one dotted leaf per selected id, in a single call, so only
those ids are touched. Setup never removes a guardrail. The cause: each id
write replaces only that id, so every id that is already in `config.toml`
and not selected stays as it is. When `config.toml` holds
`execute.guardrails` as an array of tables (`[[execute.guardrails]]`), the
tool refuses the write and writes nothing.

```
setup_write_sections({
  sectionsJson: JSON.stringify({
    "execute.guardrails.<id1>": { description: <description>, severity: <severity> },
    "execute.guardrails.<id2>": { description: <description>, severity: <severity> }
  })
}) → { ok, written, errors, next }
```

| Result of the call | Next step |
|---|---|
| `ok` is true | Step 4 |
| `ok` is false | Report the ids in `written`: these ids landed. Show each entry of `errors` and the `next` text to the user. The hand edit that `next` names is for the user. Do not edit `config.toml` by hand yourself. Skip Step 4. Stop |
| The call fails with `holds as an array of tables ([[execute.guardrails]])` | Nothing was written. Show the error and its Suggestion to the user. The user converts each `[[execute.guardrails]]` entry to a named table `[execute.guardrails.<id>]`, then runs this sub-flow again. Skip Step 4. Stop |
| The call fails because `.sdlc-v2/config.toml` is not valid TOML, the tool cannot open it, or it is over 1 MiB | Name `.sdlc-v2/config.toml`. Show the error and its Suggestion to the user. Skip Step 4. Stop |

To remove one guardrail, send `null` on
`"execute.guardrails.<id>"`. After an interrupted call, run this sub-flow
again. A write that stopped part way leaves some ids written. Each id write
replaces only that id and removes no other id, so a second run gives the
same result.

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
- Read `.sdlc-v2/config.toml` (bare Read, Glob, or Bash) to inspect the
  current `execute.guardrails` table — this sub-flow has no such read; see
  Step 0 and Gotchas.
- Send a whole-table `execute.guardrails` key. setup_write_sections refuses a
  write that drops an id. On that error, edit no file by hand.

## Gotchas

- **The Guardrail Catalog (`setup-guardrails.md`) is the source of truth for scanning.** Do not invent guardrails outside it except through the custom-guardrail path.
- **Neither mode can detect already-configured ids.** No tool that this
  sub-flow calls returns the current `execute.guardrails` table
  (`setup_write_sections` only writes; `validate({action:"guardrails",
  section:"execute"})` returns pass/fail findings, not raw guardrail data).
  `prepare_orchestrator` in harden mode (`executeGuardrails`) returns it,
  but this sub-flow does not call it, and a bare Read of
  `.sdlc-v2/config.toml` is not available to this sub-flow. If the user
  reselects an id that's already configured, that id's write overwrites its
  `description`/`severity` with the catalog's current values — this is a
  real data-loss path, not harmless idempotency. Warn the user before Stage
  A/custom (see Step 2).
- **Only a caller of a whole-table key can get `would remove guardrails`.**
  setup_write_sections returns this error when an `execute` or
  `execute.guardrails` key does not keep every id on disk. This sub-flow
  sends only one-id keys, so it never gets this error.
- **Custom guardrails need ID validation.** The kebab-case pattern `^[a-z][a-z0-9]*(-[a-z0-9]+)*$` must be enforced before writing.

## See Also

- [`/execute`](../execute/SKILL.md) — consumes execution guardrails during plan execution
- [`/setup --execution-guardrails`](../setup/SKILL.md) — parent skill that delegates execution guardrail setup to this sub-flow
- [`setup-guardrails.md`](./setup-guardrails.md) — plan guardrails analog; holds the shared Guardrail Catalog
