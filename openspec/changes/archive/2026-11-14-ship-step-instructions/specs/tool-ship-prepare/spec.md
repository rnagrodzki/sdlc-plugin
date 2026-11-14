# Spec Delta

## ADDED Requirements

### Requirement: Instructions check and seed
The tool SHALL read the `[instructions]` section from the main worktree. A config error SHALL add one `errors` entry that starts with `ship: config error, no step started.`, or `ship: a config file cannot be read, no step started.` when a file cannot be read, and the tool SHALL write no state. Otherwise the tool SHALL write one pending record for each instruction of the run keys.

- Run keys: the configured steps, received-review and commit-fixes when review is a step, and `ship`.

#### Scenario: Config error
- **WHEN** `local.toml` has `[[instructions.deploy]]`
- **THEN** `errors` has one entry that names the field `instructions.deploy`
- **AND** no state file is written

#### Scenario: Step not in the run
- **WHEN** `instructions.harden` is set and harden is not a step
- **THEN** no harden record is seeded

#### Scenario: Dry run
- **WHEN** `dryRun` is true
- **THEN** the records are seeded and none runs
