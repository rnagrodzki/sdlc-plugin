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
- A quoted or paraphrased error, `next` text, question or option text gives the cause, the recovery and the result that the source text gives. A quote is copied from the source, and a quote of each branch of one choice is checked on its own.
- A row or rule that states what a state does to calls, or how a value is set, cleared or pruned, is compared with each write path and event in the code. A row that refuses two write paths when the code refuses one, or a lifecycle rule that lists three of four events, is a finding, unless the text says it names only some.
- A list of the actions, kinds or values that a flag or mode applies (for example the edit actions of --auto), or a step that describes a dispatch, is compared with the enum or the agent file that defines it. A member, input, step or output that the source has and the text omits is a finding, unless the text says that it names only some.
