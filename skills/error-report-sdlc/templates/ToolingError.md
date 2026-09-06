## Error Summary
{what failed — one line}

## Skill Context
- **Skill**: {skill name, e.g., pr-sdlc}
- **Step**: {step number and name where the error occurred}
- **Operation**: {what the skill was trying to do, e.g., "Create PR via gh CLI"}
- **Timestamp**: {ISO timestamp}

## Error Details
- **Error type**: {script crash | CLI failure | API error | build failure | escalation}
- **Exit code / HTTP status**: {code}
- **Error message**:
  ```
  {full error text}
  ```

## Environment
- **Repository**: {repository name from git remote}
- **Branch**: {current branch}

## Reproduction
1. {what the user was doing}
2. Invoked `/{skill-name}` {with arguments if any}
3. {step that failed and why}

## Impact
{what was blocked — e.g., "PR creation blocked", "Release tag not pushed"}

## Suggested Investigation
{skill-specific hints about what might be wrong, provided by the calling skill}
