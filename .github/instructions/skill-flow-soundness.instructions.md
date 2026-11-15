---
applyTo: "plugins/sdlc/skills/**/SKILL.md"
---
# skill-flow-soundness — Review Instructions

Walks a SKILL.md's narrated step flow end-to-end to catch missing steps, unreachable branches, contradictory gates, orphaned flags, and unreachable exit states.

Default severity: high

## Verification Checklist
- A route leaves a step at its end, or names where the rest of the step goes. Each routes-table row names exactly one target step, and zero results and an errored check each have a row.
- A resume rule reads a missing record as 'not started' only if the dispatch follows the record write. Running workers are stopped or adopted, a stop rule names the record that lists running workers, and a value a later step reads after context compaction is saved in a checkpoint.
- A failed check, lane or retry limit is reported as failed, never as passed or fixed. Its gates are listed as unchecked, and a tool warning is shown to the user.
- A retry changes an input or waits on a condition. A call that fails the same way each time is not retried unchanged.
- A rule worded never, only or always is not broken by any step. A step that deletes or rewrites data that a later step reads says what the later step does when the data is gone, and a value derived from that data is recomputed or asked again.
- A stop, cancel or error exit that ends the run, including a stop in the first step, tells the user what to do next, or says that nothing is left. A cause that gives only the reason is a finding.
- A resume, restart or retry route is walked as its own entry point. Each error or refusal that the call can return has a route in every flow that makes the call, no recovery row loops back to the call that raised the error with the same input, a restart ends or adopts the workers it leaves behind, and a line that a step tells the model to read is defined.
- Each answer option of a prompt, including one that skips every item, declines or stops, has a next step and writes the same records as the other routes, and each later clean exit is reachable after it. A rule that says nothing changes before approval lists every step that changes something without an answer, and a skip or stop answer states what it does to work that an earlier rule approved.
- A step that changes one record with two or more tool calls states the record after a failed or interrupted later call and how the next entry detects it, or it makes one call.
- A sentence or bullet holds one rule with one condition; two rules that apply to different cases are split. A step with a skip decision, a tool call and a record write states their order, and says whether the record is written when the skip fires.
- A counter, flag or note in a checkpoint or run record that serves two or more gates, lanes or items holds one value for each of them. A field keyed by a step name that two gates share is a finding.
- A paragraph, rule, table row or list that names the writers of a stored value, the routes into a stop, the steps that a flag changes (an Arguments row for `--auto`) or the errors a resume flow can return names every one, or says that it names only some. Grep for each route and compare.
- A resume or restart step names how it finds each stored list or record that it reads, and says what it does with a list that an earlier exit swept.
- A condition that the user can see at two steps of one route (a tool error, a drift report, a warning list, a stop cause) is printed by one named step, and each other step on that route says that it does not print it again. A route that prints the same text twice, or prints it nowhere, is a finding.
- A field that a step reads from items of several sources (a thread, an overview, a missed-finding list) has a named source for each kind of item and a stated result for an item whose source gives none. Two or more calls in one step that feed each other state their order and what the step does when one fails. A skip set that two steps both apply lists the same members in both, or states each difference.
- A line, note or instruction that a step adds to the prompt of a dispatched agent or skill has a named step of the receiver that reads and acts on it. A line that no receiver step reads is a finding.
- A pointer, hint or offer that names another skill, tool or page restates the precondition of its target and is skipped when it fails. A step that waits for the user or a page form to write a stored value reads it back with a named call before a later step routes on it, and a route names the value that is still missing or unchanged.
