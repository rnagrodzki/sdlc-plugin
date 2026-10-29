---
name: skill-flow-soundness
description: Walks a SKILL.md's narrated step flow end-to-end to catch missing steps, unreachable branches, contradictory gates, orphaned flags, and unreachable exit states.
triggers:
  - "plugins/sdlc/skills/**/SKILL.md"
severity: high
---

## What to check

- **Step reachability.** Every step must be reachable under some valid
  input. A branch gated on a condition that can never hold — or that
  contradicts an earlier gate in the same flow — is dead.
- **Exit-state completeness.** Every terminal state the skill describes
  (success, cancel, error, stall, interrupt) must have an explicit next
  instruction. When the skill has a routes table, each terminal state needs
  its own row. A state the flow can enter but never says how to leave is
  incomplete. A recovery step in a refusal or error row must change the failing condition for every input state that reaches that row. Read the tool or flag that the step names, and list the states that reach the row (for example a new file and an existing file). A step that works for only some of these states leaves the others stuck, and it is a finding. When an error or refusal row ends the run, also read the Suggestion and Next text of the tool that returned the error. The row must follow the recovery that text names, or the prose must state why the run ends instead. A row that ends the run while the tool tells the caller to retry, or the reverse, is a finding. The next instruction of a stop, cancel or error exit that ends the run says what the user does next (for example fix an input and run the skill again), or states that nothing is left to do. This holds for a stop in the first step, before any file or state is written, and for each cause that a step lists. Read each listed cause, not only the causes that have a row. A cause that names the reason and no next action for the user is a finding.
- **Gate consistency.** A condition gating one step must not be
  contradicted by another step in the same file assuming the opposite
  (e.g. one step skips work "if `--force` was NOT passed" while a later
  step assumes `--force` is always set by that point). The same holds
  for an absolute statement (never, only, always, does not) in a rules
  list, a description or a table: no step may do what it forbids or skip
  what it requires, for example a rule that says the skill never adds a
  section heading while a step adds one. A step that breaks such a
  statement is a finding.
- **Arguments-table / Workflow parity.** Every flag documented in the
  Arguments table must be referenced by name somewhere in Workflow, and
  every flag referenced in Workflow must appear in the Arguments table.
  An orphan in either direction is a flow defect — a flag nobody reads, or
  a behavior nobody can invoke. The same parity applies to a value, note,
  marker or status that a step defines or records (for example a kind of
  kept note or a column value): some later step or tool must read it, and
  each value a step reads must be recorded by some step. A recorded value
  that nothing reads is a finding. The same parity applies to each field
  that a tool returns and that the skill documents: some step must read
  it, or the skill must state that it is informational only. A documented
  field that no step reads is a finding.
- **Used items leave the selection.** When a skill lists or picks stored items by a status or marker (for example topic files that are ready for the next skill) and a later step or skill uses an item, read what clears or changes that status. Some step must move a used item out of the selection, or the prose must say that it stays and state what a cap or a sort order of the list then does to a new item. A status that selects items and that no step clears is a finding. A capped list that puts used items ahead of new ones can hide new work, and it is a finding. This extends the status parity rule in Arguments-table / Workflow parity, which checks that a status is recorded and read, not that it ends.
- **Numbered-step drift.** After steps are renumbered or reordered, every
  cross-reference to a step number — in the same file, or in a companion
  sub-flow it dispatches — must be updated to match.
- **Step insertion impact on branch routing.** When a new step (e.g. Step
  2b) is inserted into an existing flow, verify that every conditional
  branch and routing that previously connected step N to step N+1 is still
  accurate — the inserted step may now lie in the middle. Also verify that
  bypass notes and direct-entry options that list skippable steps remain
  accurate: is the newly inserted step bypassable, or has it changed the
  mandatory-vs-optional structure of any path?
- **Approval-gate reachability.** An `AskUserQuestion` step must be
  reachable on every path that needs it. Verify no earlier branch can
  silently skip past a mandatory approval gate.
- **Per-step rule wiring.** When a skill's prose defines a general rule
  that applies to multiple steps (e.g. "each checkpoint step calls
  `plan_mark`", "every lane/lens/reviewer that records evidence has a
  fallback when `evidence_record` fails"), grep each named step body to
  verify the inline call or fallback is actually present. A blanket
  paragraph stating the rule does not satisfy a contract that names
  per-step behavior; every named step must carry its own inline
  instruction.
- **AskUserQuestion option quality.** Options offered to the user must be
  mutually exclusive, phrased without a leading/loaded framing, and (per
  this project's own tool convention) carry a real, statable default —
  not a set where only one option is actually workable. Each option must
  lead to a different next action or a different recorded value. Two
  options with the same outcome are one option written twice, and they
  are a finding. Merge them or state what differs.
- **Resource cleanup.** When a skill's flow dispatches background workers,
  sub-agents, or async tasks (e.g. Agent tool calls), verify that every
  possible exit path (success, stall, error, timeout, interrupt, cancel)
  includes an explicit
  cleanup step: a TaskStop call, an agent termination, or a documented
  reason why cleanup is not needed (e.g. workers terminate themselves).
  No worker should be left running after the skill completes or is
  interrupted. Specifically, for stall handlers (Step 3) and cleanup
  phases (Step 9), grep for TaskStop calls or equivalent termination logic
  on each dispatched Agent task. Abandoned workers are a resource leak and
  token waste.
- **Exit-path state record.** Each exit path that ends a dispatched worker
  must also record that worker's outcome in the run state (for example a
  `ledger_skip` reason). A path that stops the worker but writes no outcome
  is a finding.
- **Routing-bullet exclusivity.** When one step lists several routing bullets, or one outcome or routes table lists several rows, keyed on the same input (a round number, a verdict, a gate result, a tool result), exactly one bullet or row must match any given input state. Walk the boundary states, such as the last round, a CRITICAL gate result, or a single-item batch, against every bullet and row. A state that matches two bullets or rows with different outcomes is a finding, even when each reads correctly on its own. A state that matches none is a dead end, and it is a finding.
- **Shared-record field parity.** When two or more routes write the same
  persisted record (a round record, a history row, a state snapshot),
  every route must derive each field from the same named source, or the
  prose must state which route wins on conflict. A route that hard-codes
  a field (for example zero counts) while another route can still add
  data to that field makes the record disagree with itself, and is a
  finding. The same applies to one call described in two places with
  different arguments. The same holds when one route writes a field twice,
  or when a second pass of the same route writes a field that already holds
  a value (for example a note in a table cell): the prose must state whether
  the second write replaces the value, appends to it or is refused. A second
  write with no stated outcome that leaves two values in one field is a
  finding.
- **Stopped-worker output.** Output from a stopped, abandoned, or
  never-reported worker must not feed a consolidation or merge step
  unless that step labels it partial.
- **Stop-failure fallback.** When TaskStop or an equivalent call fails,
  the flow must name a fallback and list the worker as not stopped. A
  worker that cannot be stopped still counts toward any wave or
  concurrency cap. A cleanup step that follows an earlier failed stop must
  retry that stop before it lists the worker as not stopped.
- **Dispatch without task ID.** An Agent dispatch that returns no task ID
  is a worker that cleanup cannot end. The flow must list it as missing
  or unterminated.
- **Single return target.** Each loop or sub-flow names exactly one
  return step. Two return targets for one loop is a finding.
- **Output slots.** Each disclosed worker state (missing, unterminated,
  still running after a failed stop) has a literal slot in the output
  template.
- **Opt-in step routing.** When a config key or flag enables a step, every
  route into it from an earlier step (a go to, a next, or a skip) must
  reach it while the enabling condition holds. A route that jumps past an
  enabled step is a finding. A route that skips the step only when the
  enabling condition is off is a designed shortcut, not a finding.
- **Overview order and content.** The Steps overview and any numbered step
  list, including a header flow summary, must present steps in run order.
  Each entry must match the body step it names: same step number, same
  name, same action and tool call. An entry that describes a step the body
  no longer does, or keeps a number the body renumbered, is a finding. An
  overview that lists steps out of run order misstates the flow, so it is
  a finding.
- **Count and flag sources.** A route that counts findings (found, open,
  blocking) must count every source, including findings recorded without an
  id. A route must not advance on a flag that the fixer set on its own work
  (for example `fixed: true`) unless an independent re-check runs first. A
  count named for a good outcome (pass, ok, clean) must count only items
  that reached that outcome. A definition such as 'all other rows' takes in
  items that no check ran on, for example when the source of the check was
  unavailable. Such items need their own bucket or stay out of the count. A
  pass count that includes unchecked items is a finding. The same holds
  in the other direction: a set or count that routes the flow (for
  example the sections still empty) must leave out any item that the
  flow marks optional, or the route fires on a state that needs no
  action. Check each such definition against the tool field it names. Each
  member of a set that routes the flow to a question (for example each
  section still empty) has a defined question or a named source that fills
  it. A member with no defined question or source is a finding.
- **Delegated next routes.** A step that defers to a tool result's `next`
  string is a route. List each `next` value the tool can return, and confirm
  that each one reaches every enabled opt-in step. An offer that hands off
  to another skill or tool must restate that target's own precondition, and
  the flow must skip the offer when the precondition fails.
- **Unattended answers.** An AskUserQuestion that `--auto` or a run without
  a user can reach must state the answer the flow uses without a user. This
  extends the option-quality default rule above.
- **Resume and checkpoint inference.** Walk each interruption point of the flow, including a crash between a dispatch and the write of its checkpoint. A resume rule that reads a missing record as 'not started' is a finding unless the record is written before the dispatch. Workers found running on resume must be stopped or adopted. A foreground Agent call that timed out counts as a started worker that may still run. Each value that a later step reads after context compaction (for example a 'material change detected' flag) must be saved in a checkpoint. A value kept only in the conversation is lost.
- **Multi-call write atomicity.** When one step changes one stored record, or a set of linked records, with two or more tool calls (for example a rework that clears a section and then writes another, or one write for each answer), list the calls in order. State the record after each call, and after a failure or an interruption between two calls. When the tool accepts all the changes in one call, a step that uses two calls is a finding. When two calls stay, the step must name how the next entry detects a half-changed record and what it does: finish the change, undo it or ask the user. A step that leaves a half-changed record with no detection and no recovery is a finding. This extends Resume and checkpoint inference, which walks an interruption between a dispatch and its checkpoint, to writes that one step makes in sequence.
- **Route placement.** A route leaves a step only at the end of that step, or it names the point where the rest of the step goes. A route taken from the middle of a step that skips the rest of it is a finding (for example 'fixes done, go to Step 5' inside a fix loop). Each route row names exactly one target step. A row that names two targets is a finding. Walk zero results and an errored check as boundary states of every routes table and bullet list, together with the last round and a single-item batch.
- **Failure and warning signals.** A gate check, lane or retry that fails, errors or hits its retry limit must be reported as failed. Its gates are listed as unchecked, the final report has a slot for it, and the run records a failure, never a pass or `fixed: true`. A constant pass line that ignores a failed lane is a finding. Each field that a tool returns to report a problem (warnings, a partial-failure list, the result of a sibling check) must be read by a step and shown to the user or routed. A field that no step reads is a finding.
- **Retry discipline.** A retry is a recovery step, so the recovery-step rule in Exit-state completeness applies: it must change an input or wait on a condition that the failed call did not control. Retrying a call that fails the same way for the same input (for example a deterministic validator error) is a finding. The step must route that error class to a fix or a stop.
- **Re-entry and error-route coverage.** Walk each flow that makes the same tool call (first entry, resume, restart, retry, each variant) as its own entry point. Each error or refusal that the call can return, including one that the change adds (for example a stored run that lacks a marker), must have a route in every flow that makes the call, or the row must state why that flow cannot reach it. A route that exists in the create flow and is missing in the resume flow is a finding. A recovery row that sends the flow back to the step that raised the error, directly or around a cycle of steps, must change the input that raised it or name a retry limit and an exit. A route that can repeat the same call with the same input without end is a finding. This extends Retry discipline to cycles. A restart or re-prepare route is an exit path for Resource cleanup. It must end or adopt each worker started before it, even when the restart replaces the run state. A line, marker or value that a step tells the model to print, read or pass (for example a 'Resume with:' line) must be defined where it is first used: the text names the step or tool that writes it and gives its format. A name that is used and never defined is a finding.
- **Mid-run change of stored data.** When a step deletes, prunes, adds or rewrites data that a later step reads or routes on (a section heading, a marker line such as a cut line, a table row), walk each later step that depends on that data. Each one must say what it does when the data is missing, because an earlier step or the user may have removed it during the run. A value derived from the data (an answer the user gave, a line the skill wrote, a count) must be recomputed or asked again by a named step when a later step changes the data. Check a final pass and a re-run of the step as boundary states. A flow that can reach a state with the data missing or the value stale, and has no step that restores or re-derives it, is a finding.
- **Answer-option boundary states.** Walk each answer option that an `AskUserQuestion` or an approval prompt offers as its own route, including an option that skips every item, declines, stops or selects none. Each option has a next step. An option that skips every item leaves the loop with no item, so the clean-exit condition of each later step (for example a test for 'no finding was skipped') and the final summary must still be reachable from that state. Each term or record that the other routes write for the same outcome (a ledger status, a summary label) is written by this route too, or the prose says it is not. Where an earlier rule already approved work (a pre-approved class, an automatic fix), the prose states what a skip, decline or stop answer does to that work, and the option label matches that effect; an option worded as stopping all work while a pre-approved path still runs is a finding. A rule that says nothing changes before approval lists every step that changes something without an answer; a step that does so and is missing from the list is a finding. A route with no next step, a record that only some routes write, or an exit that the post-skip state cannot reach is a finding.

## What NOT to flag

- An optional step intentionally skipped by a documented flag — that is a
  designed shortcut, not a dead branch.
- Prose style or wording choices that don't change the actual step
  sequence — that is `skill-prose-accuracy`'s job, not this dimension's.

## Cross-references

- `skill-prose-accuracy.md` — verifies individual factual claims against
  source; this dimension verifies the shape of the flow itself
  (reachability, completeness, gate consistency), a different failure
  mode from "is this sentence true."
- `skill-cross-reference-consistency.md` — verifies claims across
  multiple skill files; this dimension is single-file, flow-shape-only.
- Guardrail: `skill-flow-soundness` (plan and execute) enforces this same
  check at plan time and again independently before a task is marked
  done.
