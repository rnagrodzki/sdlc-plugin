# Spec Delta

## ADDED Requirements

### Requirement: Standalone instructions
Without `--no-instructions`, the skill SHALL call `instructions_resolve` with key `pr` before Step 0, and SHALL run the list after Step 6 and print `Instructions (pr): …`. Only a config instruction can run git or gh commands from the skill.

#### Scenario: Success
- **WHEN** the PR is created and the pr list has 2 instructions
- **THEN** the skill runs both and prints `Instructions (pr): 2 total, …`

#### Scenario: Lease rejected
- **WHEN** `pr_apply` returns `remote branch <branch> changed since the last push. pr stopped.`
- **THEN** the skill stops and no instruction runs
