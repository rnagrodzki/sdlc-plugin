# Spec Delta

## MODIFIED Requirements

### Requirement: resolve-config action
The `resolve-config` action SHALL return the effective `auto`, `quality`, `commitWaves`, and `highRiskAutoApprove` values with per-key provenance, without reading or writing any run state file.

| Key | Resolution order | Default |
|---|---|---|
| `auto` | `auto` input (`cli`) > ship state `flags.auto` for `branch` (`pipeline`) > `executePrefs.auto` (`local`) | false (`default`) |
| `quality` | `quality` input (`cli`) > `executePrefs.quality` (`local`) > `balanced` when auto (`default`) | empty (`unset`) |
| `commitWaves` | `commitWaves` input `true`/`false` (`cli`) > config `execute.commitWaves` (`config`) | true (`default`) |
| `highRiskAutoApprove` | `executePrefs.highRiskAutoApprove` (`local`) | false (`default`) |

| Output field | Meaning |
|---|---|
| `auto` | effective auto mode |
| `quality` | `full`, `balanced`, `minimal`, or empty; empty means the skill must ask for a tier; never omitted |
| `commitWaves` | effective commit-per-wave setting |
| `highRiskAutoApprove` | effective high-risk auto-approval |
| `sources` | per key: `cli`, `pipeline`, `local`, `config`, `default`, or `unset` |
| `warnings` | non-fatal problems and the moved-keys notice |

- `executePrefs` lives in `.sdlc-v2/local.toml`; `execute.commitWaves` lives in `.sdlc-v2/config.toml`.
- `local` marks a value from `[executePrefs]` of `~/.sdlc/local.toml` merged with `.sdlc-v2/local.toml`. `config` marks a value from `.sdlc-v2/config.toml`.
- Before reading, the action moves stale `auto`, `quality`, and `highRiskAutoApprove` keys from config.toml `[execute]` to local.toml `[executePrefs]`.
- The ship-state cross-read happens only when `branch` is passed.

#### Scenario: No inputs and no config
- **WHEN** nothing sets any key
- **THEN** `auto` is false, `quality` is empty with source `unset`, `commitWaves` is true, `highRiskAutoApprove` is false

#### Scenario: Pipeline auto
- **WHEN** `branch` is passed and that branch's ship state has `flags.auto: true`
- **THEN** `auto` is true with source `pipeline`
- **AND** `quality` defaults to `balanced` with source `default` when nothing else sets it

#### Scenario: Local values labeled local
- **WHEN** `.sdlc-v2/local.toml` `[executePrefs]` sets `auto = true`, `quality = "full"` and `highRiskAutoApprove = true`
- **AND** no input sets these keys
- **THEN** `sources.auto`, `sources.quality` and `sources.highRiskAutoApprove` are each `local`

#### Scenario: commitWaves from the config file
- **WHEN** `.sdlc-v2/config.toml` `[execute]` sets `commitWaves = false`
- **AND** no `commitWaves` input is passed
- **THEN** `commitWaves` is false with source `config`

#### Scenario: Out-of-enum CLI quality
- **WHEN** `quality` is `C`
- **THEN** a warning names the value and resolution falls through to config, then the default
- **AND** the call does not fail

#### Scenario: Wrong-typed config value
- **WHEN** `executePrefs.highRiskAutoApprove` is not a bool
- **THEN** a warning names the wrong type and the built-in default is used

#### Scenario: Unreadable ship state or config
- **WHEN** the ship state or a config section cannot be read
- **THEN** a warning is added and resolution continues with defaults

#### Scenario: Stale keys moved
- **WHEN** config.toml `[execute]` holds `highRiskAutoApprove = true`
- **THEN** the key is moved to local.toml `[executePrefs]`
- **AND** `warnings` carries a notice starting `Moved personal settings from .sdlc-v2/config.toml to .sdlc-v2/local.toml`
- **AND** `sources.highRiskAutoApprove` is `local`

#### Scenario: Unsafe move
- **WHEN** the move conflicts with a different value already in local.toml
- **THEN** the tool fails with a DataError starting `Cannot move personal settings from .sdlc-v2/config.toml to .sdlc-v2/local.toml automatically`
