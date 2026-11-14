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
| Step 0: Pre-flight | tool | Calls `setup_prepare` and `setup_init`, scaffolds `.sdlc-v2/`, snapshots the current config and routes the flags to a list of section IDs. | The section list and the config snapshot | no | always |
| Step 1: Selective-section menu | user | Prints the numbered list of sections with their state. The user replies with the numbers to configure, `all`, `not-set`, `none` or `cancel`. | The selected section IDs | no | no `--only`, `--force` or direct-entry flag is given |
| Step 2: Migration | skill | Asks whether to migrate legacy config files, then runs the `migrate` tool for the import, the config check and the layout move. | The migrated config files | yes | legacy config is found, or `--migrate` is given. The user can answer no |
| Step 2b: Save-target question | user | Asks where to save personal settings: per section, this project, or the user profile. | The save target for the run | no | a selected section is stored in `local.toml` |
| Step 3: Dispatch loop | skill | Configures each selected section in order: prints the section header and options, asks one question for each field, or follows the sub-flow of the section. Shows a diff preview, asks the user to confirm it and writes the config files with `setup_write_sections`. | The written config sections | no | at least one section is selected |
| Step 3b: Validate written config | tool | Reads the config again and checks that each written section now shows as set. It warns and offers a retry when a section does not. | The validation result | no | at least one section was written |
| Step 4: Summary | skill | Shows the files created, updated or migrated, and the CI scripts that need an update. | The setup summary | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:setup`, with or without a flag. The skill stops when plan mode is active. |
| Ends | After Step 4 prints the summary. When the user selects no section, the run goes to Step 4 and changes nothing. |
| Asks the user | At the section menu, at the migration question, at the save-target question, for each field of each selected section, and at the diff preview before the write. |
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
