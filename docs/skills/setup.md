# /sdlc:setup

Configure the sdlc plugin for your project from one menu: pick a section, answer its questions, confirm the diff.

## Why use it

You installed the plugin, or a skill reported missing config, and you do not want to learn two TOML files by hand.
`/sdlc:setup` shows each config section with its current state, and you choose the sections to configure.
For each section, it explains every option, asks its questions and shows the change before it writes.
The result is the config that `/sdlc:ship`, `/sdlc:review`, `/sdlc:plan` and the other skills read.

## Example

Run the skill with no flag:

```text
/sdlc:setup
```

The skill creates `.sdlc-v2/` and its two commented config files when they are absent. Then you see:

- A status block: 19 sections, each with `[set]` or `[not set]` and a summary of its current value.
- A numbered menu. You reply with numbers such as `1,3,5`, or with `all`, `not-set` or `none`.
- For each section you chose: its purpose, the files it changes, the skills that read it, and its options.
- A before/after table of the config values that change. The skill writes them after you confirm, then lists the files it created or updated.

## What you get

- Configure only what you need: choose sections by number, take `all`, or take only the sections that are not set.
- Understand each option before you answer: each field shows its type, default, description and examples, and you can ask for more.
- See each change before it lands: a before/after table shows the changed values, and you confirm it before the write.
- Keep personal settings personal: save them for this project only, or in your user profile for every project on your machine.
- Run it again safely: setup never replaces an existing config file with the template, and a run writes only the sections you configure.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Pre-flight | tool | Calls `setup_prepare` and `setup_init` and scaffolds `.sdlc-v2/`. Then it asks the Setup tab question (item 3), snapshots the current config and routes the flags to a list of section IDs. | The section list and the config snapshot | no | always |
| Step 0 item 3: Form sections: dialogue or Setup tab | user | Asks where to change the form sections, before the config snapshot and the menu. On `Setup tab` it starts the dashboard and opens the [Setup tab](../dashboard.md#setup-tab), and the menu of this run lists only the sections that need the assistant. In a T3 Code thread, it opens the Setup tab in a T3 preview tab (`preview_open`) and prints `Setup tab: <previewUrl> (T3 preview tab)`; when that tab does not open, it prints `Setup tab: <previewUrl>` and `Warning: T3 preview tab did not open — <reason>.` Exits: a dashboard error prints `Tool error text (dashboard):`, the error and `The form sections continue here.`, and the menu lists all 19 sections; `Dialogue`, free text, a dismissed question or no question tool changes nothing. | The `formsDeferred` value for Step 1 and Step 4 | yes | no `--only`, `--force` or direct-entry flag, the main worktree is active, and no migration runs (no legacy config, no `--migrate`). It is asked for a new config and for an existing config. `--add`, `--no-copilot` and `--remove-openspec` do not change this step. |
| Step 1: Selective-section menu | user | Prints the numbered list of sections with their state: the 9 sections that need the assistant after a `Setup tab` answer, else all 19 sections. The user replies with the numbers to configure, `all`, `not-set`, `none` or `cancel`. `all` and `not-set` select only from the listed sections. | The selected section IDs | no | no `--only`, `--force` or direct-entry flag is given |
| Step 2: Migration | skill | Asks whether to migrate legacy config files, then runs the `migrate` tool for the import, the config check and the layout move. | The migrated config files | yes | legacy config is found, or `--migrate` is given. The user can answer no |
| Step 2b: Save-target question | user | Asks where to save personal settings: per section, this project, or the user profile. | The save target for the run | no | a selected section is stored in `local.toml` |
| Step 3: Dispatch loop | skill | Configures each selected section in order: prints the section header and options, asks one question for each field, or follows the sub-flow of the section. Shows a diff preview, asks the user to confirm it and writes the config files with `setup_write_sections`. Before a write, the tool checks each form field value against the field list, and a refused value writes nothing. The [Setup tab](../dashboard.md#setup-tab) of the dashboard shows and saves the fields of the form sections. It shows the other sections as read-only cards, and only `/sdlc:setup` changes them. | The written config sections | no | at least one section is selected |
| Step 3b: Validate written config | tool | Reads the config again and checks that each written section now shows as set. It warns and offers a retry when a section does not. | The validation result | no | at least one section was written |
| Step 3c: CI script update | user | Lists each CI script that is `outdated` or `missing` and asks `Update the CI scripts listed above?` with `Update (Recommended)` and `Skip`. On `Update` it calls `scaffold_ci({updateOutdated: true})` and then re-checks with `setup_prepare({skipConfigCheck: true})`. Exits: a drift check error asks no question (the summary prints it); a `scaffold_ci` error prints the tool error text and `Some CI files can already be written. Run git status to see them, then run /setup again.`; a failed re-check prints its error, and the summary prints `Run /setup again to check the CI scripts.`; free text, a dismissed question or no AskUserQuestion counts as `Skip`. | The updated CI scripts and the fresh drift result | yes | a CI script is `outdated` or `missing`. `--force` and `--only` do not skip it |
| Step 4: Summary | skill | Shows the files created, updated or migrated, and the CI scripts that are still `outdated` or `missing`. When the drift check fails, it prints `CI script check failed. Tool error text (setup_prepare):` and the error, once. It ends with a pointer to the dashboard Setup tab. After the Step 0 item 3 `Setup tab` answer, a `Deferred` line says that this run did not save the form sections, and the pointer says to save them at the address that Step 0 item 3 opened. On every other run the pointer comes from `dashboard` `status`: the address when the dashboard runs, else `/sdlc:dashboard`; a `status` error prints `Tool error text (dashboard):`, the error and the `/sdlc:dashboard` pointer. In a linked worktree it also prints `The Setup tab changes the config of the main worktree of this repo.` | The setup summary | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:setup`, with or without a flag. The skill stops when plan mode is active. |
| Ends | After Step 4 prints the summary and the Setup tab pointer. When the user selects no section, the run goes to Step 3c and changes no config file. A run that stops before Step 4 prints no pointer, for example in plan mode, after 3 invalid menu replies, or on a migration error. |
| Asks the user | At the Setup tab question (only when all Step 0 item 3 conditions hold), at the section menu, at the migration question, at the save-target question, for each field of each selected section, at the diff preview before the write, and at the CI script update question. |
| Resume | none |

## More examples

**Go directly to one section:**

```text
/sdlc:setup --dimensions
```

No menu. The skill scans your project, proposes review dimensions with evidence, and writes the dimensions you select to `.sdlc-v2/review-dimensions/`.
`--guardrails`, `--execution-guardrails`, `--pr-template`, `--plan-template` and `--openspec-enrich` also open one section directly.

**Configure a set of sections:**

```text
/sdlc:setup --only version,ship,review
```

No menu. The skill configures the three sections, then shows the diff and the summary.
`ship` and `review` are personal settings, so the skill asks one time where to save them.

**Reconfigure everything:**

```text
/sdlc:setup --force
```

No menu. The skill selects all 19 sections, including the sections that are already set.

## Config templates

`/sdlc:setup` writes `.sdlc-v2/config.toml` and `.sdlc-v2/local.toml` from the template files of the plugin.
Both files are commented TOML that lists every option, so you can also change a setting by hand.
The menu does not ask about some options. One of them is `[harden.instructions]`: see [Custom instructions](./harden/custom-instructions.md).
[Config files](./setup/config-files.md#options-that-setup-does-not-ask-about) lists the others.

## Reference

- [Flags and sections](./setup/flags-and-sections.md): each flag, the menu replies, the 19 section IDs, and how to ask what an option means.
- [Config files](./setup/config-files.md): where each setting is saved, the options that setup does not ask about, migration, and the CI script check.

## Related skills

- [/sdlc:ship](./ship.md): reads the pipeline steps and the review threshold that you set here.
- [/sdlc:review](./review.md): reads the review dimensions and the review scope that you set here.
- [/sdlc:plan](./plan.md): reads the plan template and the plan guardrails that you set here.
- [/sdlc:jira](./jira.md): reads the default Jira project key that you set here.
