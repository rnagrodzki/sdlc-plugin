# Spec Delta

## ADDED Requirements

### Requirement: Instruction rows on the page
The page SHALL show one row for each instruction under its step tile, and an end group after the last tile. Each row SHALL show a status lamp, the label, the text cut to 60 characters, and the outcome, as text only.

#### Scenario: Markup in text
- **WHEN** the text is `<img src=x onerror=alert(1)>`
- **THEN** the page shows the text as is and runs no script

#### Scenario: Long text
- **WHEN** the text has 61 characters
- **THEN** the row shows 60 characters plus `…` and the full text is in the `title` attribute
