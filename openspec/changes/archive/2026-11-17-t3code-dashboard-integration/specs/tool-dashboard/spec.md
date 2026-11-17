# Spec Delta

## ADDED Requirements

### Requirement: T3 preview address
In T3 Code, the `dashboard` tool `ensure` action SHALL return `previewUrl` and SHALL NOT open the system browser.

#### Scenario: Ensure in a T3 thread
- **WHEN** `T3CODE_THREAD_ID` is `t-1` and `ensure` runs with `open` true
- **THEN** the output has `previewUrl` `<url>#pipelines?thread=t-1`
- **AND** no system browser opens

#### Scenario: Ensure outside T3 Code
- **WHEN** `T3CODE_THREAD_ID` is not set
- **THEN** the output has no `previewUrl`
- **AND** `open` true opens the system browser as before

### Requirement: Run panel address
The `dashboard` tool `ensure` action SHALL take an optional `runId` and SHALL put it in `previewUrl`.

#### Scenario: Ensure with runId
- **WHEN** `ensure` runs in thread `t-1` with `runId` `ship-feat-x-20261116T175538Z`
- **THEN** `previewUrl` is `<url>#ship-feat-x-20261116T175538Z?thread=t-1`

#### Scenario: Bad runId
- **WHEN** `runId` does not match `^(ship|execute|plan)-[A-Za-z0-9._-]{1,200}$`
- **THEN** the tool returns a DomainError that names the value

#### Scenario: runId with tab
- **WHEN** `ensure` gets both `runId` and `tab`
- **THEN** the tool returns a DomainError
