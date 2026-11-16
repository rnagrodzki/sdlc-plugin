# Spec Delta

## ADDED Requirements

### Requirement: Short titles and full items by id
The deferred skill SHALL show each open item by its `title` (at most 80 runes) and SHALL get the full text of the chosen items with `deferred_list` `detail.ids` before it drafts an issue.

#### Scenario: Triage list
- **WHEN** the user runs `/sdlc:deferred`
- **THEN** each listed item shows its id and a title of at most 80 runes

#### Scenario: Filing an item
- **WHEN** the user chooses 2 items to file
- **THEN** the skill calls `deferred_list` with those 2 ids and uses each description as the issue body
