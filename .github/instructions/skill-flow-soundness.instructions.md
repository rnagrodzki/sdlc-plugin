---
applyTo: "plugins/sdlc/skills/**/SKILL.md"
---
# skill-flow-soundness — Review Instructions

Walks a SKILL.md's narrated step flow end-to-end to catch missing steps, unreachable branches, contradictory gates, orphaned flags, and unreachable exit states.

Default severity: high

## Verification Checklist
- A route leaves a step at its end, or names where the rest of the step goes. Each routes-table row names exactly one target step, and zero results and an errored check each have a row.
- A resume rule reads a missing record as 'not started' only if the dispatch follows the record write. Running workers are stopped or adopted, and a value a later step reads after context compaction is saved in a checkpoint.
- A failed check, lane or retry limit is reported as failed, never as passed or fixed. Its gates are listed as unchecked, and a tool warning is shown to the user.
- A retry changes an input or waits on a condition. A call that fails the same way each time is not retried unchanged.
