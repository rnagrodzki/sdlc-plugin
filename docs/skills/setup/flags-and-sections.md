# Flags and sections

Read this page when you want to skip the menu, configure one section directly, or find the ID of a section for `--only`. No flag is required.

## Example

```text
/sdlc:setup --only jira,commit
```

Skips the menu and configures the `jira` section and the `commit` section.

## Syntax

    /sdlc:setup [options]

## Flags

| Flag | What it does | Default |
|------|--------------|---------|
| `--migrate` | Runs the migration step even when setup detects no legacy config. | off |
| `--force` | Skips the menu and selects all 19 sections, including the sections that are already set. Ignored when `--only` or a direct-entry flag is also given. | off |
| `--only <ids>` | Skips the menu and configures the sections in the comma-separated list. Use the IDs from [Section IDs](#section-ids). | none |
| `--dimensions` | Goes directly to the review dimensions. Same as `--only review-dimensions`. | off |
| `--pr-template` | Goes directly to the PR template. Same as `--only pr-template`. | off |
| `--guardrails` | Goes directly to the plan guardrails. Same as `--only plan-guardrails`. | off |
| `--execution-guardrails` | Goes directly to the execution guardrails. Same as `--only execution-guardrails`. | off |
| `--plan-template` | Goes directly to the plan template. Same as `--only plan-template`. | off |
| `--openspec-enrich` | Goes directly to the OpenSpec config enrichment. Same as `--only openspec-block`. | off |
| `--remove-openspec` | Removes the managed block from `openspec/config.yaml`. Use it with `--openspec-enrich`. | off |
| `--add` | Adds new review dimensions instead of reconfiguring all of them. Use it with `--dimensions`. In the guardrail sub-flows it changes nothing: see [Guardrail flags](#guardrail-flags). | off |
| `--no-copilot` | Skips the GitHub Copilot instructions of the dimensions setup. Use it with `--dimensions`. See [Copilot mirrors](#copilot-mirrors). | off |

The six flags that go directly to one section are the direct-entry flags.

## Menu replies

With no `--only`, `--force` or direct-entry flag, setup prints the numbered menu and waits for your reply.

| Reply | Result |
|-------|--------|
| Numbers, for example `1,3,5` or `1-3,7` | The sections at those positions |
| `all`, or an empty reply | Every section |
| `not-set` | Only the sections that show `[not set]` |
| `none` or `cancel` | No section. The run configures nothing and goes to the summary |

An invalid reply prints one line and the menu again. After 3 invalid replies, the run stops and configures nothing.

## Section IDs

Setup configures the selected sections in the order of this table.

| ID | What it configures | Saved in |
|----|-------------------|----------|
| `version` | Where the version string lives | `config.toml` |
| `ship` | Ship pipeline preferences | `local.toml` |
| `jira` | Default Jira project key and allowed project list | `config.toml` |
| `review` | Review scope and settings | `local.toml` |
| `received-review` | How review feedback is processed | `local.toml` |
| `commit` | Commit message style | `config.toml` |
| `pr` | PR description settings | `config.toml` |
| `github` | Your GitHub login for the `/sdlc:pr` account check | `local.toml` |
| `pr-labels` | Auto-labeling rules | `config.toml` |
| `review-dimensions` | Review dimensions | `.sdlc-v2/review-dimensions/` |
| `pr-template` | Custom PR description template | `.sdlc-v2/pr-template.md` |
| `plan-template` | Custom plan template | `.sdlc-v2/plan-template.md` |
| `communication-style` | How every sdlc skill talks to you: reader level, writing standard, tone, language | `local.toml` |
| `plan-style` | Plan-only style: visual density, extra writing rules, custom plan instructions | `local.toml` |
| `plan-tasks` | Plan task defaults | `config.toml` |
| `plan-guardrails` | Plan guardrail rules | `config.toml` |
| `execution-guardrails` | Execution guardrail rules | `config.toml` |
| `openspec-block` | OpenSpec configuration | `openspec/config.yaml` |
| `automation` | Unattended-run behavior for `/sdlc:ship` and `/sdlc:execute` | `local.toml` |

A section that is saved in `local.toml` is a personal setting. See [Where personal settings are saved](./config-files.md#where-personal-settings-are-saved).

## Guardrail flags

These rules apply to `--guardrails` and `--execution-guardrails`.

- `--add` and the default mode behave the same.
- The guardrail sub-flows write one id at a time and never remove a guardrail.
- The sub-flows do not check which ids are already configured. A reselected id overwrites its description and severity.
- When the tool refuses a write, the sub-flow stops. There is no bypass. The tool refuses when the guardrails section is an array of tables, `[[plan.guardrails]]`.
- To remove a guardrail on purpose, send JSON null on `<section>.guardrails.<id>` with `setup_write_sections`.

## Copilot mirrors

Without `--no-copilot`, Step 8 of the dimensions setup writes one Copilot mirror for each dimension.

- Each mirror ends with an `<!-- sdlc:generated sha256=<hash> -->` marker line.
- Setup passes `overwrite: true` only for a mirror whose overwrite you confirmed.
- Without `overwrite`, the tool refuses a mirror with no valid marker (`has hand-written content`), and setup skips that mirror.
- To recover, move the hand-written rows into the dimension file. Then call `dimensions_render_instructions` for that dimension with `overwrite: true`.

## Asking what an option means

The `Options:` block of each section lists every field with its type, default, description and one or more `Examples:` lines.

If a prompt is still unclear, tell `/sdlc:setup` that you want an explanation instead of an answer. Setup calls `setup_prepare({ explain: "<section>.<field>" })`, shows the returned details and examples, then asks the same question again. There is no retry limit: ask for an explanation until you are ready to answer.

## Tips

- **Run it one time for each project.** After the first setup, you need `/sdlc:setup` again only to add dimensions, change settings or migrate a legacy config.
- **Use a direct flag to save time.** `--dimensions`, `--guardrails` or `--only <id>` goes directly to what you need.
- **You do not need `--migrate` in advance.** When setup finds legacy or outdated config, it asks whether to migrate. See [Migration](./config-files.md#migration).

Back to the [/sdlc:setup overview](../setup.md).
