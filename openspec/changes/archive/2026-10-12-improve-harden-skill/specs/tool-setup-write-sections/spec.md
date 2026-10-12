# Spec Delta

## MODIFIED Requirements

### Requirement: Wholesale write at the named key
The tool SHALL replace the table at the named key wholesale and SHALL leave every other key in the file unchanged. For a dotted key it SHALL replace only the leaf table, keep siblings under the same parent, and create missing parent tables.

- A `null` value on `plan.guardrails.<id>` or `execute.guardrails.<id>` deletes that table. A missing table changes nothing, and the summary says `<id>: not present, nothing deleted`.
- A guardrail table that holds a comment line is not deleted: the call returns the comment refusal.
- A `null` guardrail id with `target: "user"` returns a DomainError `guardrail ids live in config.toml`.
- A `null` value on any other key writes an empty table at that key, which clears it.
- Callers pass the complete object for the key; the tool does not patch individual fields.
- A key with an empty dotted segment (e.g. `plan.`, `.guardrails`, `plan..tasks`) fails with `config: invalid section name "<key>"` in `errors`.

#### Scenario: Other sections kept
- **WHEN** `config.toml` has a `version` table
- **AND** the call writes `{"jira":{"defaultProject":"TEST"}}`
- **THEN** `version` is unchanged
- **AND** `jira.defaultProject` is `TEST`

#### Scenario: Dotted leaf keeps siblings
- **WHEN** `config.toml` has `plan.guardrails`
- **AND** the call writes `{"plan.tasks":{...}}`
- **THEN** `plan.guardrails` is unchanged

#### Scenario: Null clears a leaf
- **WHEN** the call writes `{"plan.tasks":null}`
- **THEN** `plan.tasks` becomes an empty table

#### Scenario: Null deletes a guardrail
- **WHEN** `config.toml` has `[plan.guardrails.dry]` and `[plan.guardrails.dry-old]`
- **AND** the call writes `{"plan.guardrails.dry-old":null}`
- **THEN** the `[plan.guardrails.dry-old]` block is gone
- **AND** the `[plan.guardrails.dry]` block is byte-identical
