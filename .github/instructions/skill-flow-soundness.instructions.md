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
- A rule worded never, only or always is not broken by any step. A step that deletes or rewrites data that a later step reads says what the later step does when the data is gone, and a value derived from that data is recomputed or asked again.
- A stop, cancel or error exit that ends the run, including a stop in the first step, tells the user what to do next, or says that nothing is left. A cause that gives only the reason is a finding.
