# Config files

Read this page when you want to know which file holds a setting, where personal settings go, or which options you edit by hand.

## The three files

| File | Holds | In git |
|------|-------|--------|
| `.sdlc-v2/config.toml` | The project config | committed |
| `.sdlc-v2/local.toml` | Your personal preferences, for this project only | gitignored |
| `~/.sdlc/local.toml` | Your personal preferences, shared across every project on your machine | outside the repository |

Set `$SDLC_USER_CONFIG` to use a different path for the user-profile file.

`/sdlc:setup` writes `.sdlc-v2/config.toml` and `.sdlc-v2/local.toml` verbatim from the template files of the plugin, `plugins/sdlc/templates/config.toml` and `plugins/sdlc/templates/local.toml`. Both are plain, commented TOML. Open them when you want to see every available option before, or instead of, the interactive flow. Setup leaves an existing file unchanged. The short version of this paragraph is in [Config templates](../setup.md#config-templates).

## Where personal settings are saved

Seven sections are personal settings, not project settings: `ship`, `review`, `received-review`, `communication-style`, `plan-style`, `github` and `automation`. When a run of `/sdlc:setup` configures one or more of them, it asks one time where to save your answers:

1. **Defaults per section (recommended)**: `communication-style`, `plan-style`, `review`, `received-review`, `automation` and `github` go to `~/.sdlc/local.toml`. `ship` goes to `.sdlc-v2/local.toml` of this project.
2. **All to this project**: every personal section goes to `.sdlc-v2/local.toml`. Other projects on your machine do not see them.
3. **All to your user profile**: every personal section goes to `~/.sdlc/local.toml`, or to `$SDLC_USER_CONFIG` if you set it. A key that is already set in `.sdlc-v2/local.toml` of this project still overrides the user-profile value, for this project only.

The status block of the menu marks each personal section `[set] (user)`, `[set] (project)` or `[set] (user+project)`. The mark shows which file the current values come from.

Setup does not save the answer to this question. A later run that configures a personal section asks again.

## Options that setup does not ask about

Edit these options by hand. Each option is in `.sdlc-v2/config.toml`, except `[instructions]`, which can be in all three config files.

| Option | What it does | More |
|--------|--------------|------|
| `[instructions]` | Follow-up instructions that run after a step, after a ship run, or after a skill. Valid in `.sdlc-v2/config.toml`, `.sdlc-v2/local.toml` and the user-level file | [Follow-up instructions](../../instructions.md) |
| `[harden.instructions]` | The custom instructions for `/sdlc:harden` | [Custom instructions](../harden/custom-instructions.md) |
| `[version.pushAuth]` | A custom CI push-auth secret, for example when branch-protection rulesets block the default token. The `version` section of the config template documents it | [Protected branches and rulesets](../../versioning.md#protected-branches-and-rulesets) |
| `baseBranch` in `[git]` (default `""`) | The integration branch for `execute`, `ship`, `pr`, `review` and `commit`. Empty means the default branch of the repository | [Integration branch](../../getting-started.md#integration-branch) |
| `baseSync` in `[execute]` (default `true`) | Controls whether `execute` merges `origin/<base>` into the branch between waves | none |

An explicit `--base` flag on `pr` or `review` still wins over `baseBranch`. `execute`, `ship` and `commit` have no `--base` flag and always use the resolved value.

## Migration

When setup finds legacy or outdated config, it asks whether to migrate before it configures a section. The menu also shows one warning line above the status block.

- **yes**: setup imports the config, the templates, the Jira templates, the learnings and the review dimensions from the old data directory into `.sdlc-v2/`. It never overwrites a key that you already changed. Then it checks the config version and moves an old `.sdlc-v2/execution/` directory to `.sdlc-v2/runs/`.
- **no**: you configure from scratch. Setup leaves the legacy files untouched.

`--migrate` runs this step when setup detects no legacy config.

## CI script drift detection

`/sdlc:setup`, and each skill that calls `setup_prepare`, compares each installed CI scaffold script and workflow with the version that the plugin bundles. The result is `ciScriptDrift`: one entry for each scaffolded script, flagged `current`, `outdated` or `missing`, with the installed version and the current version.

The summary of a setup run lists each script that is not `current`.

- **Update all of them:** run `scaffold_ci({force: true})`. It overwrites the outdated scripts with the bundled versions.
- **Check drift only:** call `validate({action: "ci_script_drift"})`. It does not touch the sections of `/sdlc:setup`.

Back to the [/sdlc:setup overview](../setup.md).
