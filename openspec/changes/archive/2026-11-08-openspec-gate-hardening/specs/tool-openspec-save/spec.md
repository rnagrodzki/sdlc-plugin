# Spec Delta

## ADDED Requirements

### Requirement: Saved files are checked on disk and in the git index
After `git add`, `openspec_save` SHALL check that each `stage.json` file is on disk and in the git index, and that no file under `openspec/changes/<change>/` is untracked. A gap SHALL return DomainError `openspec_save: <n> file(s) of change "<change>" are missing or not in the git index: <paths>`. `.openspec.yaml` is never reported.

#### Scenario: Untracked file
- **WHEN** `openspec/changes/a/notes.md` is untracked after `git add`
- **THEN** `openspec_save` returns DomainError that names `openspec/changes/a/notes.md`

#### Scenario: Clean save
- **WHEN** every manifest file is on disk and in the git index
- **THEN** `openspec_save` returns no warning and no error for the check
