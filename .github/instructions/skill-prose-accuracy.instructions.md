---
applyTo: "plugins/sdlc/skills/**/SKILL.md"
---
# skill-prose-accuracy — Review Instructions

Validates that SKILL.md prose descriptions accurately reflect actual implementation via direct grep/read, not inferred from build/test success alone.

Default severity: high

## Verification Checklist
- A verb such as evaluates, validates or enforces matches what the code does, and a prune or garbage-collection claim names each case the code keeps.
- A quoted or paraphrased error gives the cause and the recovery that the code text gives.
- A positional reference such as 'the fourth value' matches the canonical list.
- When a change replaces a check or a call with another, each step that relied on the old one names the tool and action that now does the job. A step that says 'check again' with no named call is a finding.
