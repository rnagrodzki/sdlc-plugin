# /setup

Configure the SDLC plugin for your project. Walks you through an interactive
menu to set up versioning, ship pipeline, review dimensions, Jira, PR
templates, plan templates, guardrails, and more.

## When to use

- You just installed the plugin and need to configure it for your project.
- A skill reported "missing config" — run `/setup` to fix it.
- You want to add or change review dimensions, guardrails, or pipeline settings.
- You are migrating from a legacy configuration.

## Syntax

    /setup [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--migrate` | Migrate legacy config files. | off |
| `--skip <section>` | Skip a config section (use section IDs from the table below). | none |
| `--force` | Reconfigure all sections, including ones already set. | off |
| `--only <ids>` | Comma-separated section IDs to configure directly. | none |
| `--dimensions` | Jump to review dimensions setup. | off |
| `--pr-template` | Jump to PR template setup. | off |
| `--guardrails` | Jump to plan guardrails setup. | off |
| `--execution-guardrails` | Jump to execution guardrails setup. | off |
| `--plan-template` | Jump to plan template setup. | off |
| `--openspec-enrich` | Jump to OpenSpec config enrichment. | off |
| `--remove-openspec` | Remove the managed block from `openspec/config.yaml` (use with `--openspec-enrich`). | off |
| `--add` | Add new items instead of reconfiguring all (for dimensions, guardrails). | off |
| `--no-copilot` | Skip copilot instructions section. | off |

### Section IDs

| ID | What it configures |
|----|-------------------|
| `version` | Where the version string lives |
| `ship` | Ship pipeline preferences |
| `jira` | Default Jira project key and allowed project list |
| `review` | Review scope and settings |
| `received-review` | How review feedback is processed |
| `commit` | Commit message style |
| `pr` | PR description settings |
| `pr-labels` | Auto-labeling rules |
| `review-dimensions` | Review dimensions |
| `pr-template` | Custom PR description template |
| `plan-template` | Custom plan template |
| `plan-style` | Plan style preferences |
| `plan-tasks` | Plan task defaults |
| `plan-guardrails` | Plan guardrail rules |
| `execution-guardrails` | Execution guardrail rules |
| `openspec-block` | OpenSpec configuration |

## Examples

**Full interactive setup:**

    /setup

Shows a menu of all sections with their status. Pick which to configure.

**Configure only review dimensions:**

    /setup --dimensions

**Add new guardrails to existing ones:**

    /setup --guardrails --add

**Configure specific sections:**

    /setup --only version,ship,review

**Reconfigure everything:**

    /setup --force

## CI script drift detection

Running `/setup` (or any skill that calls `setup_prepare`) also compares
every CI scaffold script/workflow already installed in your repo against the
version bundled with the plugin, and reports the result as `ciScriptDrift`:
one entry per scaffolded script, each flagged `current`, `outdated`, or
`missing`, with the installed and current version numbers.

- **Remediate all of them at once:** re-run `scaffold_ci({force: true})` (or
  `/setup` and re-confirm the `ship` section) to overwrite outdated scripts
  with the bundled versions.
- **Check drift independently**, without touching `/setup`'s other sections,
  by calling `validate({action: "ci_script_drift"})`.

## Config templates

`/setup` writes `.sdlc-v2/config.toml` and `.sdlc-v2/local.toml` verbatim
from the plugin's own template files at `plugins/sdlc/templates/config.toml`
and `plugins/sdlc/templates/local.toml`. Both are plain, fully-commented TOML
— open them directly if you want to see every available option (including
ones `/setup`'s menu doesn't prompt for) before or instead of running the
interactive flow.

If your project uses the `push-with-secret` release method, its
`[version.pushAuth]` section is documented in the `version` section of the
config template and in [the versioning
docs](../versioning.md#the-push-with-secret-method).

## Related skills

- Every skill depends on `/setup` for its config. "Missing config" errors point
  here.
- [/ship](ship.md) — Pipeline steps and review threshold configured here.
- [/review](review.md) — Dimensions and scope configured here.
- [/plan](plan.md) — Plan template and guardrails configured here.
- [/jira](jira.md) — Jira project key configured here.

## Tips and gotchas

- **Run once per project.** After initial setup, you only need `/setup` again
  to add dimensions, change settings, or migrate legacy config.
- **Section shortcuts save time.** Use `--dimensions`, `--guardrails`, or
  `--only <id>` to jump to what you need.
- **Config location.** Project config: `.sdlc-v2/config.toml` (committed).
  Local preferences: `.sdlc-v2/local.toml` (gitignored).
- **Migration is usually automatic.** If legacy config is detected, skills tell
  you to run `--migrate`. You do not need to guess in advance.
