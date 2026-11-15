# Spec Delta

## ADDED Requirements

### Requirement: Field value check
Before any write, the tool SHALL check each new value of a form field against the field list. A refused value SHALL stop the call with a `DomainError` that names each field, its form section id, its config path, and what the field accepts. No file SHALL change.

#### Scenario: Bad new value
- **WHEN** a call sets `ship.bump` to `huge` and the target file holds `bump = "patch"`
- **THEN** the call returns a `DomainError` that names `ship.bump` and the options `patch`, `minor`, `major`
- **AND** the bytes of each file stay the same

#### Scenario: Section id in the error
- **WHEN** a call sets a bad `style.tone`
- **THEN** the error names the section `communication-style` and the path `style.tone`

#### Scenario: Unreadable target file
- **WHEN** the target file is larger than the size limit of the check
- **THEN** the call returns a `DataError` that names the file, its size, and the limit

### Requirement: Values with no check
The check SHALL pass, with no check, a value that equals the value in the target file, a JSON null, and a key with no form field.

#### Scenario: Stored value passes
- **WHEN** a call sends `ship.bump = "rc"` and the target file already holds `bump = "rc"`
- **THEN** the check does not refuse `ship.bump`

#### Scenario: Null value passes
- **WHEN** a call sends `ship.bump` as JSON null
- **THEN** the check does not refuse `ship.bump`

### Requirement: Limits of one value
The check SHALL refuse a new value that is outside the limits of its field type. The limits come from the `type`, `options`, `min` and `max` of the field in the field list.

#### Scenario: Boolean field
- **WHEN** a new value of a `boolean` field is not `true` or `false`
- **THEN** the check refuses the value

#### Scenario: Enum field
- **WHEN** a new value of an `enum` field is a text that is not in `options`
- **THEN** the check refuses the value

#### Scenario: Multi-enum field
- **WHEN** a new value of a `multi-enum` field is not a list, holds an entry that is not in `options`, or holds an entry twice
- **THEN** the check refuses the value

#### Scenario: Number field
- **WHEN** a new value of a `number` field is not a whole number, is below `min`, or is above `max`
- **THEN** the check refuses the value
- **AND** with no `max`, the check refuses a value above 2147483647

#### Scenario: String field
- **WHEN** a new value of a `string` field is not a text, or is a text of more than 1024 bytes
- **THEN** the check refuses the value

#### Scenario: List field
- **WHEN** a new value of a `list` field is not a list of texts, has more than 64 entries, or has an entry of more than 1024 bytes
- **THEN** the check refuses the value

#### Scenario: Push secret name
- **WHEN** a new value of the `version` field `pushAuth.secretName` is a name that the CI script generator does not accept
- **THEN** the check refuses the value
- **AND** an empty text passes
