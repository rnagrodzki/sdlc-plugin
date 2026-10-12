# Spec Delta

## MODIFIED Requirements

### Requirement: Main flow ordering
The skill SHALL run classify, pre-flight, consent gate 1, `prepare_orchestrator`, orchestrator dispatch, consent gate 2, duplicate search, then issue creation, with both gates and all `gh` writes in the main context.

Main flow when no duplicate issue exists:

```mermaid
sequenceDiagram
    participant User
    participant Skill as error-report skill
    participant gh
    participant git
    participant PO as prepare_orchestrator
    participant Orch as Subagent error-report-orchestrator
    Skill->>gh: gh auth status
    Skill->>git: git remote get-url origin
    Skill->>User: Create one? yes or no
    User-->>Skill: yes
    Skill->>PO: mode error_report, error fields
    PO-->>Skill: manifestPath, mode, similarIssues empty
    Skill->>Orch: MANIFEST_FILE and PROJECT_ROOT
    Orch-->>Skill: title and body JSON
    Skill->>User: show proposal, yes or edit or cancel
    User-->>Skill: yes
    Skill->>gh: gh issue create
    gh-->>Skill: issue URL
    Skill->>User: GitHub issue created
```

#### Scenario: Normal creation
- **WHEN** both gates get **yes** and no duplicate is found
- **THEN** the skill prints `GitHub issue created: #<number> — <url>`

### Requirement: Duplicate issue search
After gate 2 **yes**, the skill SHALL read `similarIssues` and `similarIssuesNote` from the `prepare_orchestrator` result, and SHALL NOT run `gh issue list` itself.

- `similarIssuesNote` set: print it, then go to issue creation without a prompt.
- No match: go to issue creation without a prompt.
- Matches: ask **comment**, **new** or **cancel**.
- **comment**: user picks an issue, approves the body `Additional occurrence reported by error-report:` plus the proposal body, then the skill runs `gh issue comment <number> --repo "rnagrodzki/sdlc-plugin" --body ...` and prints `Comment added to #<number> — <url>`.
- **cancel** at any point: remove the manifest, create nothing.

#### Scenario: Similar issue exists
- **WHEN** `similarIssues` holds an open issue titled `[plan] Gate A dispatch timeout` and the skill is `plan`
- **THEN** the skill offers **comment**, **new** and **cancel**

#### Scenario: Search failed
- **WHEN** `similarIssuesNote` is `duplicate search skipped: gh not found`
- **THEN** the skill prints the note
- **AND** it goes to issue creation without a prompt
